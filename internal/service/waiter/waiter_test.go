package waiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/waiter"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type env struct {
	clock *servicetest.Clock
	execs *servicetest.Executions
	queue *servicetest.Queue
	svc   *waiter.Service
	ids   func() domain.ExecutionID
}

func newEnv(t *testing.T, mutate ...func(*config.WorkerConfig)) *env {
	t.Helper()
	clock := servicetest.NewClock()
	store := servicetest.NewStore(clock)
	e := &env{clock: clock, execs: store.Executions(), queue: servicetest.NewQueue(clock, 3)}
	ids := servicetest.NewIDs(clock)
	e.ids = func() domain.ExecutionID {
		id, err := ids.ExecutionID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	cfg := config.Default().Worker
	cfg.WaiterInterval = 10 * time.Millisecond
	for _, m := range mutate {
		m(&cfg)
	}
	var err error
	e.svc, err = waiter.New(cfg, waiter.Deps{
		Executions: e.execs, States: e.execs, Queue: e.queue, Tx: store, Clock: clock, IDs: ids, Codec: servicetest.Codec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// waiting stores an execution paused on node "Wait".
func (e *env) waiting(t *testing.T, until time.Time, token string) domain.ExecutionID {
	t.Helper()
	id := e.ids()
	ex := domain.Execution{ID: id, WorkflowID: "wf_x", WorkflowVersion: 1, Mode: domain.ModeManual, Status: domain.StatusWaiting, StartedAt: e.clock.Now(),
		RunData: map[string][]domain.NodeRun{"Wait": {{Status: domain.NodeRunWaiting}}}}
	if !until.IsZero() {
		ex.WaitUntil = &until
	}
	if err := e.execs.Create(context.Background(), ex); err != nil {
		t.Fatal(err)
	}
	st := &engine.State{ExecutionID: id, Status: domain.StatusWaiting, RunData: ex.RunData,
		Pending: &engine.PendingWait{Node: "Wait", Until: until, ResumeToken: token, Output: [][]domain.Item{{{JSON: map[string]any{"orig": true}}}}}}
	blob, _ := servicetest.Codec{}.EncodeState(st)
	if err := e.execs.SaveState(context.Background(), id, blob); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTickQueuesOnlyDueExecutions(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	due := e.waiting(t, e.clock.Now().Add(time.Minute), "")
	later := e.waiting(t, e.clock.Now().Add(time.Hour), "")
	signal := e.waiting(t, time.Time{}, nodesdk.NewResumeToken("ex_x", "s"))

	if n, err := e.svc.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("nothing is due yet: n=%d err=%v", n, err)
	}
	e.clock.Advance(2 * time.Minute)
	n, err := e.svc.Tick(ctx)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	ex, _ := e.execs.Get(ctx, due)
	if ex.Status != domain.StatusQueued || ex.WaitUntil != nil || len(ex.RunData["Wait"]) != 1 {
		t.Fatalf("resumed execution = %+v", ex)
	}
	jobs := e.queue.Jobs()
	if len(jobs) != 1 || jobs[0].ExecutionID != due {
		t.Fatalf("jobs = %+v", jobs)
	}
	for _, id := range []domain.ExecutionID{later, signal} {
		if ex, _ := e.execs.Get(ctx, id); ex.Status != domain.StatusWaiting {
			t.Fatalf("%s must keep waiting, is %s", id, ex.Status)
		}
	}
	// A second tick does not queue it again: it is no longer waiting.
	if n, _ := e.svc.Tick(ctx); n != 0 || len(e.queue.Jobs()) != 1 {
		t.Fatalf("second tick: n=%d jobs=%d", n, len(e.queue.Jobs()))
	}
}

func TestTickHonoursBatchSize(t *testing.T) {
	e := newEnv(t, func(c *config.WorkerConfig) { c.WaiterBatchSize = 2 })
	for range 3 {
		e.waiting(t, e.clock.Now().Add(time.Second), "")
	}
	e.clock.Advance(time.Minute)
	if n, _ := e.svc.Tick(context.Background()); n != 2 {
		t.Fatalf("n = %d", n)
	}
	if n, _ := e.svc.Tick(context.Background()); n != 1 {
		t.Fatalf("the rest comes next tick, n = %d", n)
	}
}

func TestResumeByTokenDeliversItems(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.ids()
	token := nodesdk.NewResumeToken(string(id), "secret")
	// Rebuild the fixture with a known id.
	ex := domain.Execution{ID: id, WorkflowID: "wf_x", WorkflowVersion: 1, Mode: domain.ModeManual, Status: domain.StatusWaiting, StartedAt: e.clock.Now()}
	_ = e.execs.Create(ctx, ex)
	st := &engine.State{ExecutionID: id, Status: domain.StatusWaiting, Pending: &engine.PendingWait{Node: "Wait", ResumeToken: token}}
	blob, _ := servicetest.Codec{}.EncodeState(st)
	_ = e.execs.SaveState(ctx, id, blob)

	if err := e.svc.ResumeByToken(ctx, token, []domain.Item{{JSON: map[string]any{"approved": true}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.execs.Get(ctx, id)
	if got.Status != domain.StatusQueued || len(e.queue.Jobs()) != 1 {
		t.Fatalf("execution = %+v jobs = %d", got, len(e.queue.Jobs()))
	}
	saved, _ := e.execs.LoadState(ctx, id)
	_, state, _ := servicetest.Codec{}.DecodeSaved(saved)
	if !state.Pending.Resumed || state.Pending.ResumeItems[0].JSON["approved"] != true {
		t.Fatalf("pending = %+v", state.Pending)
	}
	// The same token cannot resume twice.
	if err := e.svc.ResumeByToken(ctx, token, nil); !errors.Is(err, waiter.ErrNotWaiting) {
		t.Fatalf("second resume: %v", err)
	}
}

func TestResumeByTokenRejectsBadTokens(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.ids()
	token := nodesdk.NewResumeToken(string(id), "secret")
	_ = e.execs.Create(ctx, domain.Execution{ID: id, WorkflowID: "wf_x", Status: domain.StatusWaiting, StartedAt: e.clock.Now()})
	st := &engine.State{ExecutionID: id, Status: domain.StatusWaiting, Pending: &engine.PendingWait{Node: "Wait", ResumeToken: token}}
	blob, _ := servicetest.Codec{}.EncodeState(st)
	_ = e.execs.SaveState(ctx, id, blob)

	for name, tok := range map[string]string{
		"empty":              "",
		"no separator":       string(id),
		"not an id":          "nope.secret",
		"unknown execution":  nodesdk.NewResumeToken("ex_01J00000000000000000000000", "secret"),
		"wrong secret":       nodesdk.NewResumeToken(string(id), "guess"),
		"prefix of the real": token[:len(token)-1],
	} {
		t.Run(name, func(t *testing.T) {
			if err := e.svc.ResumeByToken(ctx, tok, nil); !errors.Is(err, waiter.ErrInvalidToken) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if ex, _ := e.execs.Get(ctx, id); ex.Status != domain.StatusWaiting || len(e.queue.Jobs()) != 0 {
		t.Fatal("a bad token must change nothing")
	}
}

func TestLoopResumesDueExecutionsAndStopsOnShutdown(t *testing.T) {
	e := newEnv(t)
	id := e.waiting(t, e.clock.Now().Add(time.Second), "")
	e.clock.Advance(time.Minute)
	if err := e.svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	servicetest.Eventually(t, "the execution to be queued", func() bool {
		ex, _ := e.execs.Get(context.Background(), id)
		return ex.Status == domain.StatusQueued
	})
	if err := e.svc.Start(context.Background()); err == nil {
		t.Fatal("second Start must fail")
	}
	if err := e.svc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown twice: %v", err)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := waiter.New(config.Default().Worker, waiter.Deps{}); err == nil {
		t.Fatal("empty deps must fail")
	}
}
