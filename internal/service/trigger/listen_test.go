package trigger_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const fakeListenType = "test.listen"

// fakeListen is a ListenTrigger that emits one event (twice, with the same
// key) and then waits for its context to end.
type fakeListen struct {
	rec *listenRecorder
}

type listenRecorder struct {
	started atomic.Int32
	stopped atomic.Int32
	fail    atomic.Bool
	ready   chan struct{}
	mu      sync.Mutex
	overFn  []bool
}

func newRecorder() *listenRecorder { return &listenRecorder{ready: make(chan struct{}, 16)} }

func (f fakeListen) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: fakeListenType, Version: 1, DisplayName: "Fake listen", Group: nodesdk.GroupTrigger,
		Outputs:     []nodesdk.PortSpec{{Name: "main"}},
		Credentials: []nodesdk.CredentialRef{{Name: "cred", Type: "test", Required: true}},
	}
}

func (fakeListen) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, nil
}

func (fakeListen) CheckListen(params map[string]any) error {
	if params["bad"] == true {
		return errors.New("bad parameters")
	}
	return nil
}

func (f fakeListen) Listen(ctx context.Context, in nodesdk.ListenInput, emit func(context.Context, nodesdk.ListenEvent) error) error {
	f.rec.started.Add(1)
	defer f.rec.stopped.Add(1)
	if f.rec.fail.Load() {
		return errors.New("broker unreachable")
	}
	ev := nodesdk.ListenEvent{Items: []nodesdk.Item{{JSON: map[string]any{"n": float64(1)}}}, DedupKey: "t:0:7"}
	for range 2 {
		if err := emit(ctx, ev); err != nil {
			return err
		}
	}
	f.rec.mu.Lock()
	f.rec.overFn = append(f.rec.overFn, in.Overloaded(ctx))
	f.rec.mu.Unlock()
	f.rec.ready <- struct{}{}
	<-ctx.Done()
	return nil
}

func listenNode(name string, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: fakeListenType, TypeVersion: 1, Parameters: params,
		Credentials: map[string]domain.CredentialID{"cred": "c1"}}
}

func listenEnv(t *testing.T, rec *listenRecorder, sleeper port.Sleeper, q port.QueueStats) *env {
	t.Helper()
	e := newEnvWith(t, []registry.Factory{func() nodesdk.Node { return fakeListen{rec: rec} }}, sleeper, q)
	e.creds["c1"] = true
	return e
}

func waitReady(t *testing.T, rec *listenRecorder) {
	t.Helper()
	select {
	case <-rec.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not start")
	}
}

func TestListenerRunsWithoutLeadershipAndDedupsEvents(t *testing.T) {
	rec := newRecorder()
	e := listenEnv(t, rec, blockingSleeper{}, nil)
	ctx := context.Background()
	if err := e.svc.Start(ctx); err != nil { // the lease never calls back: this instance never leads
		t.Fatal(err)
	}
	wf := e.save(ctx, t, listenNode("Kafka", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	waitReady(t, rec)
	if e.svc.Leading() {
		t.Fatal("instance must not lead")
	}
	if n := e.starter.count(); n != 1 {
		t.Fatalf("executions = %d, want 1 (second emit is a duplicate)", n)
	}
	got := e.starter.reqs[0]
	if got.DedupKey != "Kafka:t:0:7" || got.StartNode != "Kafka" || got.Mode != domain.ModeTrigger {
		t.Errorf("start request = %+v", got)
	}
	if err := e.svc.Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if rec.stopped.Load() != 1 {
		t.Errorf("listener stopped %d times after Deactivate, want 1", rec.stopped.Load())
	}
	if err := e.svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownStopsListeners(t *testing.T) {
	rec := newRecorder()
	e := listenEnv(t, rec, blockingSleeper{}, nil)
	ctx := context.Background()
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	wf := e.save(ctx, t, listenNode("Kafka", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	waitReady(t, rec)
	if err := e.svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.stopped.Load() != 1 {
		t.Errorf("listener stopped %d times, want 1", rec.stopped.Load())
	}
}

func TestListenerRestartsAfterFailure(t *testing.T) {
	rec := newRecorder()
	rec.fail.Store(true)
	e := listenEnv(t, rec, instantSleeper{}, nil)
	ctx := context.Background()
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	wf := e.save(ctx, t, listenNode("Kafka", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for rec.started.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rec.started.Load() < 3 {
		t.Fatalf("listener started %d times, want a restart after each failure", rec.started.Load())
	}
	rec.fail.Store(false)
	waitReady(t, rec)
	if err := e.svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestActivateValidatesListeners(t *testing.T) {
	rec := newRecorder()
	e := listenEnv(t, rec, blockingSleeper{}, nil)
	ctx := context.Background()
	bad := e.save(ctx, t, listenNode("Kafka", map[string]any{"bad": true}))
	if err := e.svc.Activate(ctx, bad.ID); err == nil {
		t.Error("invalid parameters must fail activation")
	}
	noCred := listenNode("Kafka", nil)
	noCred.Credentials = nil
	if err := e.svc.Activate(ctx, e.save(ctx, t, noCred).ID); err == nil {
		t.Error("a missing credential must fail activation")
	}
	missing := listenNode("Kafka", nil)
	missing.Credentials = map[string]domain.CredentialID{"cred": "gone"}
	if err := e.svc.Activate(ctx, e.save(ctx, t, missing).ID); err == nil {
		t.Error("an unknown credential must fail activation")
	}
}

type instantSleeper struct{}

func (instantSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	time.Sleep(time.Millisecond)
	return ctx.Err()
}

type fakeDepth struct {
	mu    sync.Mutex
	depth int
	calls int
}

func (f *fakeDepth) Depth(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.depth, nil
}

func (f *fakeDepth) set(n int) {
	f.mu.Lock()
	f.depth = n
	f.mu.Unlock()
}

func TestBackpressureHasHysteresisAndSamplesAtAnInterval(t *testing.T) {
	q := &fakeDepth{}
	rec := newRecorder()
	e := listenEnv(t, rec, blockingSleeper{}, q)
	over := e.svc.Overloaded
	ctx := context.Background()
	step := func(depth int, want bool) {
		t.Helper()
		q.set(depth)
		e.clock.Advance(e.cfg.Kafka.DepthCheckInterval)
		if got := over(ctx); got != want {
			t.Fatalf("depth %d: overloaded = %v, want %v", depth, got, want)
		}
	}
	pause, resume := e.cfg.Kafka.PauseAboveDepth, e.cfg.Kafka.ResumeBelowDepth
	step(pause, false)    // at the threshold is not above it
	step(pause+1, true)   // above: pause
	step(pause-1, true)   // between the thresholds: stay paused
	step(resume, true)    // not yet below the resume threshold
	step(resume-1, false) // below: resume
	step(pause, false)    // between again: stay resumed
	calls := q.calls
	over(ctx)
	over(ctx)
	if q.calls != calls {
		t.Errorf("depth sampled %d extra times inside one interval", q.calls-calls)
	}
}
