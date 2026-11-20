package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type env struct {
	svc   *execution.Service
	store *servicetest.Store
	execs *servicetest.Executions
	queue *servicetest.Queue
	bus   *servicetest.Bus
	clock *servicetest.Clock
	wf    domain.Workflow
}

func newEnv(t *testing.T, mutate ...func(*config.WorkerConfig)) *env {
	t.Helper()
	clock := servicetest.NewClock()
	store := servicetest.NewStore(clock)
	e := &env{store: store, execs: store.Executions(), queue: servicetest.NewQueue(clock, 3), bus: servicetest.NewBus(), clock: clock}
	cfg := config.Default().Worker
	cfg.SyncRunTimeout, cfg.SyncPollInterval = time.Second, 10*time.Millisecond
	for _, m := range mutate {
		m(&cfg)
	}
	ids := servicetest.NewIDs(clock)
	wfID, err := ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	e.wf, err = store.Save(context.Background(), domain.Workflow{
		ID: wfID, Name: "demo",
		Nodes: []domain.Node{{Name: "Start", Type: "t.trigger", TypeVersion: 1}, {Name: "Sub", Type: "engi.trigger.executeWorkflow", TypeVersion: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc, err = execution.New(cfg, execution.Deps{
		Workflows: store, Versions: store, Executions: e.execs, States: e.execs, Queue: e.queue, Tx: store, Bus: e.bus,
		Clock: clock, IDs: ids, Codec: servicetest.Codec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) start(t *testing.T, mutate ...func(*execution.StartRequest)) domain.ExecutionID {
	t.Helper()
	req := execution.StartRequest{WorkflowID: e.wf.ID, Mode: domain.ModeManual, StartNode: "Start",
		Items: []domain.Item{{JSON: map[string]any{"a": 1.0}}}}
	for _, m := range mutate {
		m(&req)
	}
	id, err := e.svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

func TestStartStoresQueuedExecutionStateAndJob(t *testing.T) {
	e := newEnv(t)
	id := e.start(t)
	ex, err := e.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status != domain.StatusQueued || ex.WorkflowVersion != 1 || ex.Mode != domain.ModeManual || ex.StartedAt.IsZero() {
		t.Fatalf("execution = %+v", ex)
	}
	jobs := e.queue.Jobs()
	if len(jobs) != 1 || jobs[0].ExecutionID != id {
		t.Fatalf("jobs = %+v", jobs)
	}
	blob, err := e.execs.LoadState(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	in, st, err := servicetest.Codec{}.DecodeSaved(blob)
	if err != nil || st != nil || in.StartNode != "Start" || in.Items[0].JSON["a"] != 1.0 {
		t.Fatalf("saved = %+v %+v %v", in, st, err)
	}
}

func TestStartValidatesRequest(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name   string
		mutate func(*execution.StartRequest)
		is     error
	}{
		{"bad mode", func(r *execution.StartRequest) { r.Mode = "sideways" }, nil},
		{"unknown start node", func(r *execution.StartRequest) { r.StartNode = "Nope" }, nil},
		{"unknown workflow", func(r *execution.StartRequest) { r.WorkflowID = "wf_01J00000000000000000000000" }, domain.ErrNotFound},
		{"unknown version", func(r *execution.StartRequest) { r.Version = 9 }, domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := execution.StartRequest{WorkflowID: e.wf.ID, Mode: domain.ModeManual, StartNode: "Start"}
			tt.mutate(&req)
			_, err := e.svc.Start(context.Background(), req)
			var ve *domain.ValidationError
			if tt.is != nil && !errors.Is(err, tt.is) || tt.is == nil && !errors.As(err, &ve) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if len(e.queue.Jobs()) != 0 {
		t.Fatal("a rejected start must not enqueue")
	}
}

func TestDuplicateDedupKeyReturnsExistingID(t *testing.T) {
	e := newEnv(t)
	first := e.start(t, func(r *execution.StartRequest) { r.DedupKey = "order-1" })
	second, err := e.svc.Start(context.Background(), execution.StartRequest{
		WorkflowID: e.wf.ID, Mode: domain.ModeWebhook, StartNode: "Start", DedupKey: "order-1",
	})
	var dup *domain.DuplicateError
	if !errors.Is(err, domain.ErrDuplicate) || !errors.As(err, &dup) || second != first || dup.ExistingID != first {
		t.Fatalf("id = %s (want %s), err = %v", second, first, err)
	}
	if len(e.queue.Jobs()) != 1 {
		t.Fatalf("a duplicate must not enqueue a second job: %d", len(e.queue.Jobs()))
	}
}

func TestStartPinsRequestedVersion(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.Save(context.Background(), e.wf); err != nil { // version 2
		t.Fatal(err)
	}
	cur := e.start(t)
	old := e.start(t, func(r *execution.StartRequest) { r.Version = 1 })
	if ex, _ := e.svc.Get(context.Background(), cur); ex.WorkflowVersion != 2 {
		t.Fatalf("current version = %d", ex.WorkflowVersion)
	}
	if ex, _ := e.svc.Get(context.Background(), old); ex.WorkflowVersion != 1 {
		t.Fatalf("pinned version = %d", ex.WorkflowVersion)
	}
}

func TestCancel(t *testing.T) {
	t.Run("queued execution is canceled at once", func(t *testing.T) {
		e := newEnv(t)
		id := e.start(t)
		if err := e.svc.Cancel(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		ex, _ := e.svc.Get(context.Background(), id)
		if ex.Status != domain.StatusCanceled || ex.FinishedAt == nil || ex.Error == nil || ex.Error.Kind != domain.ErrorKindCanceled {
			t.Fatalf("execution = %+v", ex)
		}
		if e.execs.HasState(id) {
			t.Fatal("state must be dropped")
		}
		if got := e.bus.Events(port.EventCancelRequested); len(got) != 1 || got[0].ExecutionID != id {
			t.Fatalf("events = %+v", got)
		}
	})
	t.Run("running execution is only announced", func(t *testing.T) {
		e := newEnv(t)
		id := e.start(t)
		ex, _ := e.svc.Get(context.Background(), id)
		ex.Status = domain.StatusRunning
		e.execs.SetExecution(ex)
		if err := e.svc.Cancel(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.svc.Get(context.Background(), id); got.Status != domain.StatusRunning {
			t.Fatalf("the worker ends it, status = %s", got.Status)
		}
		if len(e.bus.Events(port.EventCancelRequested)) != 1 {
			t.Fatal("cancel must be announced")
		}
	})
	t.Run("finished execution cannot be canceled", func(t *testing.T) {
		e := newEnv(t)
		id := e.start(t)
		ex, _ := e.svc.Get(context.Background(), id)
		ex.Status = domain.StatusSuccess
		e.execs.SetExecution(ex)
		if err := e.svc.Cancel(context.Background(), id); !errors.Is(err, execution.ErrNotCancelable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown execution", func(t *testing.T) {
		e := newEnv(t)
		if err := e.svc.Cancel(context.Background(), "ex_01J00000000000000000000000"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
}

func failedExecution(e *env, t *testing.T) domain.ExecutionID {
	t.Helper()
	id := e.start(t, func(r *execution.StartRequest) { r.Items = []domain.Item{{JSON: map[string]any{"x": 2.0}}} })
	ex, _ := e.svc.Get(context.Background(), id)
	ex.Status = domain.StatusError
	ex.RunData = map[string][]domain.NodeRun{
		"Start": {{RunIndex: 0, Sources: []domain.Source{{}}, Input: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"x": 2.0}}}}}}},
		"Other": {{RunIndex: 0, Sources: []domain.Source{{Node: "Start"}}, Input: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"y": 1.0}}}}}}},
	}
	e.execs.SetExecution(ex)
	return id
}

func TestRetryStartsNewExecutionFromTheSameInput(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	orig := failedExecution(e, t)
	id, err := e.svc.Retry(ctx, orig)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := e.svc.Get(ctx, id)
	if id == orig || ex.Mode != domain.ModeRetry || ex.Status != domain.StatusQueued || ex.WorkflowVersion != 1 {
		t.Fatalf("retry = %+v", ex)
	}
	blob, _ := e.execs.LoadState(ctx, id)
	in, _, _ := servicetest.Codec{}.DecodeSaved(blob)
	if in.StartNode != "Start" || in.Items[0].JSON["x"] != 2.0 {
		t.Fatalf("retry input = %+v", in)
	}
}

func TestRetryRefusesRunningAndNeverStarted(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if _, err := e.svc.Retry(ctx, e.start(t)); !errors.Is(err, execution.ErrNotRetryable) {
		t.Fatalf("queued: %v", err)
	}
	id := e.start(t)
	ex, _ := e.svc.Get(ctx, id)
	ex.Status = domain.StatusCrashed
	e.execs.SetExecution(ex)
	if _, err := e.svc.Retry(ctx, id); !errors.Is(err, execution.ErrNotRetryable) {
		t.Fatalf("no run data: %v", err)
	}
}

func TestList(t *testing.T) {
	e := newEnv(t)
	e.start(t)
	e.start(t)
	res, err := e.svc.List(context.Background(), port.ExecutionFilter{WorkflowID: e.wf.ID}, port.Page{})
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

// finishLater plays the worker: it ends the execution after a pause.
func finishLater(e *env, id domain.ExecutionID, status domain.ExecutionStatus) {
	go func() {
		time.Sleep(30 * time.Millisecond)
		ex, _ := e.execs.Get(context.Background(), id)
		ex.Status = status
		e.execs.SetExecution(ex)
		_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventExecutionFinished, ExecutionID: id, Status: status})
	}()
}

func TestRunSyncWaitsForTheResult(t *testing.T) {
	e := newEnv(t)
	done := make(chan domain.Execution, 1)
	go func() {
		ex, err := e.svc.RunSync(context.Background(), execution.StartRequest{WorkflowID: e.wf.ID, Mode: domain.ModeWebhook, StartNode: "Start"})
		if err != nil {
			t.Error(err)
		}
		done <- ex
	}()
	servicetest.Eventually(t, "the job", func() bool { return len(e.queue.Jobs()) == 1 })
	finishLater(e, e.queue.Jobs()[0].ExecutionID, domain.StatusSuccess)
	select {
	case ex := <-done:
		if ex.Status != domain.StatusSuccess {
			t.Fatalf("status = %s", ex.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunSync did not return")
	}
}

func TestRunSyncTimesOut(t *testing.T) {
	e := newEnv(t, func(c *config.WorkerConfig) { c.SyncRunTimeout = 50 * time.Millisecond })
	_, err := e.svc.RunSync(context.Background(), execution.StartRequest{WorkflowID: e.wf.ID, Mode: domain.ModeWebhook, StartNode: "Start"})
	if !errors.Is(err, execution.ErrSyncTimeout) {
		t.Fatalf("err = %v", err)
	}
}

func TestAwaitReturnsAnAlreadyFinishedExecution(t *testing.T) {
	e := newEnv(t)
	id := e.start(t)
	ex, _ := e.svc.Get(context.Background(), id)
	ex.Status = domain.StatusError
	e.execs.SetExecution(ex)
	got, err := e.svc.Await(context.Background(), id)
	if err != nil || got.Status != domain.StatusError {
		t.Fatalf("got %+v err %v", got, err)
	}
}

type inlineFake struct {
	e      *env
	status domain.ExecutionStatus
	out    []domain.Item
	err    error
}

func (f *inlineFake) RunInline(ctx context.Context, id domain.ExecutionID) error {
	ex, _ := f.e.execs.Get(ctx, id)
	ex.Status = f.status
	if f.out != nil {
		ex.RunData = map[string][]domain.NodeRun{"Sub": {{Status: domain.NodeRunSuccess, Output: domain.PortData{domain.PortMain: {f.out}}}}}
	}
	f.e.execs.SetExecution(ex)
	return f.err
}

func TestSubworkflowsRunInlineAndReturnLastOutput(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	inline := &inlineFake{e: e, status: domain.StatusSuccess, out: []domain.Item{{JSON: map[string]any{"r": 1.0}}}}
	subs, err := execution.NewSubworkflows(e.svc, inline, "engi.trigger.executeWorkflow")
	if err != nil {
		t.Fatal(err)
	}
	res, err := subs.RunSubworkflow(ctx, nodesdk.SubworkflowCall{
		WorkflowID: string(e.wf.ID), ParentExecutionID: "ex_parent", WaitForResult: true, Items: []nodesdk.Item{{JSON: map[string]any{"in": true}}},
	})
	if err != nil || res.Status != "success" || len(res.Items) != 1 || res.Items[0].JSON["r"] != 1.0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if len(e.queue.Jobs()) != 0 {
		t.Fatal("a waited-for child must not go through the queue")
	}
	child, _ := e.svc.Get(ctx, domain.ExecutionID(res.ExecutionID))
	if child.Mode != domain.ModeSubworkflow || child.ParentExecutionID == nil || *child.ParentExecutionID != "ex_parent" {
		t.Fatalf("child = %+v", child)
	}
	blob, _ := e.execs.LoadState(ctx, child.ID)
	if in, _, _ := (servicetest.Codec{}).DecodeSaved(blob); in.StartNode != "Sub" || in.Items[0].JSON["in"] != true {
		t.Fatalf("child input = %+v", in)
	}
}

func TestSubworkflowsFireAndForgetQueues(t *testing.T) {
	e := newEnv(t)
	subs, _ := execution.NewSubworkflows(e.svc, &inlineFake{e: e}, "engi.trigger.executeWorkflow")
	res, err := subs.RunSubworkflow(context.Background(), nodesdk.SubworkflowCall{WorkflowID: string(e.wf.ID), ParentExecutionID: "ex_p"})
	if err != nil || res.Status != "queued" || len(e.queue.Jobs()) != 1 {
		t.Fatalf("res = %+v err = %v jobs = %d", res, err, len(e.queue.Jobs()))
	}
}

func TestSubworkflowsNeedATriggerNode(t *testing.T) {
	e := newEnv(t)
	subs, _ := execution.NewSubworkflows(e.svc, &inlineFake{e: e}, "engi.trigger.somethingElse")
	_, err := subs.RunSubworkflow(context.Background(), nodesdk.SubworkflowCall{WorkflowID: string(e.wf.ID)})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := execution.New(config.Default().Worker, execution.Deps{}); err == nil {
		t.Fatal("empty deps must fail")
	}
	bad := config.Default().Worker
	bad.Concurrency = 0
	if _, err := execution.New(bad, execution.Deps{}); err == nil {
		t.Fatal("bad config must fail")
	}
}
