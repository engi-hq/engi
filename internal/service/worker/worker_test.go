package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/worker"
)

// runnerFunc adapts a function to worker.Runner.
type runnerFunc func(ctx context.Context, req engine.Request) (*engine.State, error)

func (f runnerFunc) Run(ctx context.Context, req engine.Request) (*engine.State, error) {
	return f(ctx, req)
}

type env struct {
	clock  *servicetest.Clock
	store  *servicetest.Store
	execs  *servicetest.Executions
	queue  *servicetest.Queue
	bus    *servicetest.Bus
	svc    *execution.Service
	worker *worker.Worker
	wf     domain.Workflow
	wcfg   config.WorkerConfig
	qcfg   config.QueueConfig
}

type options struct {
	runner      runnerFunc
	concurrency int
	grace       time.Duration
	lease       time.Duration
	attempts    int
	failures    worker.FailureHandler
	logger      *slog.Logger
	metrics     port.Metrics
}

func newEnv(t *testing.T, o options) *env {
	t.Helper()
	if o.concurrency == 0 {
		o.concurrency = 2
	}
	if o.grace == 0 {
		o.grace = 2 * time.Second
	}
	if o.lease == 0 {
		o.lease = 10 * time.Second
	}
	if o.attempts == 0 {
		o.attempts = 3
	}
	clock := servicetest.NewClock()
	store := servicetest.NewStore(clock)
	e := &env{clock: clock, store: store, execs: store.Executions(), queue: servicetest.NewQueue(clock, o.attempts), bus: servicetest.NewBus()}
	ids := servicetest.NewIDs(clock)

	wcfg := config.Default().Worker
	wcfg.Concurrency, wcfg.ShutdownGrace, wcfg.ReclaimInterval = o.concurrency, o.grace, time.Hour
	qcfg := config.Default().Queue
	qcfg.LeaseDuration, qcfg.HeartbeatInterval, qcfg.PollInterval, qcfg.RetryDelay = o.lease, 10*time.Millisecond, 5*time.Millisecond, 0
	e.wcfg, e.qcfg = wcfg, qcfg

	wfID, err := ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	e.wf, _ = store.Save(context.Background(), domain.Workflow{ID: wfID, Name: "demo", Nodes: []domain.Node{{Name: "Start", Type: "t.trigger", TypeVersion: 1}}})
	e.svc, err = execution.New(wcfg, execution.Deps{
		Workflows: store, Versions: store, Executions: e.execs, States: e.execs, Queue: e.queue, Tx: store, Bus: e.bus,
		Clock: clock, IDs: ids, Codec: servicetest.Codec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.worker, err = worker.New(wcfg, qcfg, worker.Deps{
		WorkerID: "wk_test", Dispatcher: e.queue, Queue: e.queue, Reclaimer: e.queue, Executions: e.execs, States: e.execs,
		Versions: store, Tx: store, Bus: e.bus, Clock: clock, Engine: o.runner, Codec: servicetest.Codec{}, Failures: o.failures,
		Logger: o.logger, Metrics: o.metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) start(t *testing.T) domain.ExecutionID {
	t.Helper()
	id, err := e.svc.Start(context.Background(), execution.StartRequest{
		WorkflowID: e.wf.ID, Mode: domain.ModeManual, StartNode: "Start", Items: []domain.Item{{JSON: map[string]any{"a": 1.0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) begin(t *testing.T) {
	t.Helper()
	if err := e.worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.worker.Shutdown(ctx)
	})
}

func (e *env) status(id domain.ExecutionID) domain.ExecutionStatus {
	ex, err := e.execs.Get(context.Background(), id)
	if err != nil {
		return ""
	}
	return ex.Status
}

func (e *env) waitStatus(t *testing.T, id domain.ExecutionID, want domain.ExecutionStatus) {
	t.Helper()
	servicetest.Eventually(t, "status "+string(want), func() bool { return e.status(id) == want })
}

func success(_ context.Context, req engine.Request) (*engine.State, error) {
	return &engine.State{
		ExecutionID: req.ExecutionID, Status: domain.StatusSuccess, FinishedAt: time.Date(2026, 5, 6, 8, 0, 0, 0, time.UTC),
		RunData: map[string][]domain.NodeRun{"Start": {{RunIndex: 0, Status: domain.NodeRunSuccess}}},
	}, nil
}

func TestRunsAJobToSuccess(t *testing.T) {
	var got engine.Request
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		got = req
		req.Observer.NodeStarted(ctx, "Start", 0)
		req.Observer.NodeFinished(ctx, "Start", domain.NodeRun{RunIndex: 0, Status: domain.NodeRunSuccess})
		return success(ctx, req)
	}})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)

	ex, _ := e.execs.Get(context.Background(), id)
	if ex.FinishedAt == nil || len(ex.RunData["Start"]) != 1 || ex.Error != nil {
		t.Fatalf("execution = %+v", ex)
	}
	if got.StartNode != "Start" || got.Mode != domain.ModeManual || got.Workflow.ID != e.wf.ID || got.Workflow.Version != 1 ||
		got.Depth != 0 || len(got.StartItems) != 1 || got.Resume != nil {
		t.Fatalf("request = %+v", got)
	}
	if e.execs.HasState(id) {
		t.Fatal("the saved input must be dropped once the execution finished")
	}
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

	var kinds []port.EventKind
	for _, ev := range e.bus.Events() {
		if ev.ExecutionID == id {
			kinds = append(kinds, ev.Kind)
		}
	}
	want := []port.EventKind{port.EventNodeStarted, port.EventNodeFinished, port.EventExecutionFinished}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", kinds, want)
		}
	}
	fin := e.bus.Events(port.EventExecutionFinished)[0]
	if fin.Status != domain.StatusSuccess || fin.WorkflowID != e.wf.ID {
		t.Fatalf("finish event = %+v", fin)
	}
}

func TestFailedWorkflowIsStoredAsAnError(t *testing.T) {
	e := newEnv(t, options{runner: func(_ context.Context, req engine.Request) (*engine.State, error) {
		return &engine.State{ExecutionID: req.ExecutionID, Status: domain.StatusError, RunData: map[string][]domain.NodeRun{},
			Error: &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "Start", Message: "boom"}}, nil
	}})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusError)
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Error == nil || ex.Error.Message != "boom" || ex.FinishedAt == nil {
		t.Fatalf("execution = %+v", ex)
	}
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
}

func TestUnusableRequestFailsTheExecutionWithoutRetry(t *testing.T) {
	e := newEnv(t, options{runner: func(context.Context, engine.Request) (*engine.State, error) {
		return nil, &domain.WorkflowError{Message: "start node does not exist"}
	}})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusError)
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Error == nil || ex.Error.Kind != domain.ErrorKindWorkflow {
		t.Fatalf("error = %+v", ex.Error)
	}
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
}

func TestWaitingExecutionKeepsItsState(t *testing.T) {
	until := time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC)
	e := newEnv(t, options{runner: func(_ context.Context, req engine.Request) (*engine.State, error) {
		return &engine.State{
			ExecutionID: req.ExecutionID, Status: domain.StatusWaiting,
			RunData: map[string][]domain.NodeRun{"Start": {{Status: domain.NodeRunWaiting}}},
			Pending: &engine.PendingWait{Node: "Start", Until: until},
		}, nil
	}})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusWaiting)
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.WaitUntil == nil || !ex.WaitUntil.Equal(until) || ex.FinishedAt != nil {
		t.Fatalf("execution = %+v", ex)
	}
	blob, err := e.execs.LoadState(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if in, st, err := (servicetest.Codec{}).DecodeSaved(blob); err != nil || in != nil || st.Pending.Node != "Start" {
		t.Fatalf("saved = %+v %+v %v", in, st, err)
	}
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if fin := e.bus.Events(port.EventExecutionFinished); len(fin) != 1 || fin[0].Status != domain.StatusWaiting {
		t.Fatalf("finish events = %+v", fin)
	}
}

func TestResumesFromSavedState(t *testing.T) {
	var resumed atomic.Pointer[engine.State]
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		resumed.Store(req.Resume)
		return success(ctx, req)
	}})
	id := e.start(t)
	saved := &engine.State{ExecutionID: id, Status: domain.StatusWaiting, Pending: &engine.PendingWait{Node: "Start"},
		RunData: map[string][]domain.NodeRun{"Start": {{Status: domain.NodeRunWaiting}}}}
	blob, _ := servicetest.Codec{}.EncodeState(saved)
	_ = e.execs.SaveState(context.Background(), id, blob)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	if st := resumed.Load(); st == nil || st.Pending == nil || st.Pending.Node != "Start" {
		t.Fatalf("resume state = %+v", st)
	}
}

func TestChildExecutionKnowsItsDepth(t *testing.T) {
	// Three executions run on concurrent workers in any order, so keep the
	// deepest depth seen instead of the last one written.
	var depth atomic.Int64
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		for d := int64(req.Depth); ; {
			cur := depth.Load()
			if cur >= d || depth.CompareAndSwap(cur, d) {
				break
			}
		}
		return success(ctx, req)
	}})
	parent := e.start(t)
	pex, _ := e.execs.Get(context.Background(), parent)
	grand := pex.ID
	child := e.startChild(t, &grand)
	cex, _ := e.execs.Get(context.Background(), child)
	cid := cex.ID
	e.startChild(t, &cid)
	e.begin(t)
	servicetest.Eventually(t, "depth 2", func() bool { return depth.Load() == 2 })
}

func (e *env) startChild(t *testing.T, parent *domain.ExecutionID) domain.ExecutionID {
	t.Helper()
	id, err := e.svc.Start(context.Background(), execution.StartRequest{
		WorkflowID: e.wf.ID, Mode: domain.ModeSubworkflow, StartNode: "Start", ParentExecutionID: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSkipsExecutionsAlreadyFinished(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		calls.Add(1)
		return success(ctx, req)
	}})
	id := e.start(t)
	if err := e.svc.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.begin(t)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 0 || e.status(id) != domain.StatusCanceled {
		t.Fatalf("calls = %d, status = %s", calls.Load(), e.status(id))
	}
}

// blockUntilStopped is a runner that behaves like the engine on cancellation:
// it returns a canceled state when its context ends.
func blockUntilStopped(started chan<- struct{}) runnerFunc {
	var once sync.Once
	return func(ctx context.Context, req engine.Request) (*engine.State, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return &engine.State{ExecutionID: req.ExecutionID, Status: domain.StatusCanceled, RunData: map[string][]domain.NodeRun{},
			Error: &domain.ErrorInfo{Kind: domain.ErrorKindCanceled, Message: "execution canceled"}}, nil
	}
}

func TestCancelStopsARunningExecution(t *testing.T) {
	started := make(chan struct{})
	e := newEnv(t, options{runner: blockUntilStopped(started)})
	id := e.start(t)
	e.begin(t)
	<-started
	e.waitStatus(t, id, domain.StatusRunning)
	if err := e.svc.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, id, domain.StatusCanceled)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if e.execs.HasState(id) {
		t.Fatal("state must be dropped")
	}
}

func TestShutdownWaitsForRunningExecutionsWithinGrace(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		close(started)
		<-release
		return success(ctx, req)
	}})
	id := e.start(t)
	e.begin(t)
	<-started
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	if err := e.worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.status(id) != domain.StatusSuccess || len(e.queue.Jobs()) != 0 {
		t.Fatalf("status = %s, jobs = %+v", e.status(id), e.queue.Jobs())
	}
}

func TestShutdownAfterGraceReleasesTheJob(t *testing.T) {
	started := make(chan struct{})
	e := newEnv(t, options{runner: blockUntilStopped(started), grace: 30 * time.Millisecond})
	id := e.start(t)
	e.begin(t)
	<-started
	if err := e.worker.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs := e.queue.Jobs()
	if len(jobs) != 1 || jobs[0].Status != domain.JobQueued || jobs[0].Attempts != 0 {
		t.Fatalf("jobs = %+v", jobs)
	}
	if e.status(id) != domain.StatusQueued {
		t.Fatalf("an interrupted execution must go back to queued, got %s", e.status(id))
	}
	if !e.execs.HasState(id) {
		t.Fatal("the input must survive for the next worker")
	}
}

func TestInfrastructureErrorsRetryThenCrash(t *testing.T) {
	e := newEnv(t, options{runner: success, attempts: 2})
	id := e.start(t)
	if err := e.execs.DeleteState(context.Background(), id); err != nil { // nothing to run from
		t.Fatal(err)
	}
	e.begin(t)
	e.waitStatus(t, id, domain.StatusCrashed)
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Error == nil || ex.FinishedAt == nil {
		t.Fatalf("execution = %+v", ex)
	}
	jobs := e.queue.Jobs()
	if len(jobs) != 1 || jobs[0].Status != domain.JobFailed || jobs[0].Attempts != 2 {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestReclaimedJobRunsAgain(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, options{runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		calls.Add(1)
		return success(ctx, req)
	}, lease: time.Minute})
	id := e.start(t)
	// A worker claims the job and dies without finishing it.
	job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The dead worker's claim: the run is held by the job.
	if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: job.ID, Attempt: job.Attempts}, e.clock.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	e.begin(t)
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("the live lease must keep other workers away")
	}

	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 1 {
		t.Fatalf("the execution must complete once, ran %d times", calls.Load())
	}
	if err := e.queue.Complete(context.Background(), job.ID, "wk_dead"); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("the dead worker must have lost its lease: %v", err)
	}
}

// reclaimMetrics records JobReclaimed outcomes.
type reclaimMetrics struct {
	port.NopMetrics
	mu       sync.Mutex
	outcomes []string
}

func (m *reclaimMetrics) JobReclaimed(outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outcomes = append(m.outcomes, outcome)
}

func TestReclaimLogsAndCountsEachJob(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attempts  int
		outcome   string
		level     slog.Level
		wantLogMs string
	}{
		{"requeued", 3, port.ReclaimRequeued, slog.LevelWarn, "job lease expired, job requeued"},
		{"exhausted", 1, port.ReclaimFailed, slog.LevelError, "job lost its worker too often, marking execution crashed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			m := &reclaimMetrics{}
			e := newEnv(t, options{runner: success, attempts: tc.attempts, metrics: m,
				logger: slog.New(slog.NewJSONHandler(&buf, nil))})
			id := e.start(t)
			job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			e.clock.Advance(2 * time.Minute)
			if err := e.worker.ReclaimOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(m.outcomes) != 1 || m.outcomes[0] != tc.outcome {
				t.Fatalf("outcomes = %v, want [%s]", m.outcomes, tc.outcome)
			}
			var rec map[string]any
			if err := json.Unmarshal(bytes.SplitN(buf.Bytes(), []byte("\n"), 2)[0], &rec); err != nil {
				t.Fatalf("log = %q: %v", buf.String(), err)
			}
			want := map[string]any{
				"level": tc.level.String(), "msg": tc.wantLogMs, "job": string(job.ID), "execution": string(id),
				"previous_owner": "wk_dead", "attempts": float64(1),
			}
			for k, v := range want {
				if rec[k] != v {
					t.Errorf("log %s = %v, want %v (record %v)", k, rec[k], v, rec)
				}
			}
		})
	}
}

func TestReclaimMarksExhaustedExecutionsCrashed(t *testing.T) {
	e := newEnv(t, options{runner: success, attempts: 1})
	id := e.start(t)
	job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: job.ID, Attempt: job.Attempts}, e.clock.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}

	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.status(id) != domain.StatusCrashed {
		t.Fatalf("status = %s", e.status(id))
	}
	if fin := e.bus.Events(port.EventExecutionFinished); len(fin) != 1 || fin[0].Status != domain.StatusCrashed {
		t.Fatalf("events = %+v", fin)
	}
}

func TestHeartbeatKeepsTheLeaseAlive(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	e := newEnv(t, options{lease: time.Minute, runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		close(started)
		<-release
		return success(ctx, req)
	}})
	e.start(t)
	e.begin(t)
	<-started
	before := e.queue.Jobs()[0].LeaseUntil
	e.clock.Advance(20 * time.Second)
	servicetest.Eventually(t, "a heartbeat", func() bool { return e.queue.Jobs()[0].LeaseUntil.After(before.Add(10 * time.Second)) })
	close(release)
}

func TestLostLeaseStopsTheRunAndLeavesTheJobToItsNewOwner(t *testing.T) {
	var calls atomic.Int64
	started := make(chan struct{})
	e := newEnv(t, options{concurrency: 1, lease: time.Minute, runner: func(ctx context.Context, req engine.Request) (*engine.State, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done() // the first run is cut off by the lost lease
			return &engine.State{ExecutionID: req.ExecutionID, Status: domain.StatusCanceled, RunData: map[string][]domain.NodeRun{}}, nil
		}
		return success(ctx, req)
	}})
	id := e.start(t)
	e.begin(t)
	<-started
	// Time passes without a heartbeat getting through, and the lease is taken.
	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, id, domain.StatusSuccess)
	if calls.Load() != 2 {
		t.Fatalf("runs = %d", calls.Load())
	}
}

func TestRunInlineRunsWithoutAJob(t *testing.T) {
	e := newEnv(t, options{runner: success})
	id, err := e.svc.Start(context.Background(), execution.StartRequest{WorkflowID: e.wf.ID, Mode: domain.ModeSubworkflow, StartNode: "Start"})
	if err != nil {
		t.Fatal(err)
	}
	// Take the queued job away: an inline run does not need it.
	job, _ := e.queue.Claim(context.Background(), "someone", time.Minute)
	_ = e.queue.Complete(context.Background(), job.ID, "someone")
	if err := e.worker.RunInline(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if e.status(id) != domain.StatusSuccess {
		t.Fatalf("status = %s", e.status(id))
	}
}

func TestStartTwiceAndShutdownBeforeStart(t *testing.T) {
	e := newEnv(t, options{runner: success})
	if err := e.worker.Shutdown(context.Background()); !errors.Is(err, worker.ErrNotStarted) {
		t.Fatalf("err = %v", err)
	}
	e.begin(t)
	if err := e.worker.Start(context.Background()); err == nil {
		t.Fatal("second Start must fail")
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := worker.New(config.Default().Worker, config.Default().Queue, worker.Deps{}); err == nil {
		t.Fatal("empty deps must fail")
	}
}

// failureLog records the executions a worker reported as failed.
type failureLog struct {
	mu  sync.Mutex
	got []domain.Execution
}

func (f *failureLog) ExecutionFailed(_ context.Context, ex domain.Execution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, ex)
}

func (f *failureLog) all() []domain.Execution {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Execution(nil), f.got...)
}

func TestFailureHandlerHearsOfErrorsOnce(t *testing.T) {
	fl := &failureLog{}
	e := newEnv(t, options{failures: fl, runner: func(_ context.Context, req engine.Request) (*engine.State, error) {
		return &engine.State{ExecutionID: req.ExecutionID, Status: domain.StatusError, RunData: map[string][]domain.NodeRun{},
			Error: &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "Start", Message: "boom"}}, nil
	}})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusError)
	servicetest.Eventually(t, "the failure report", func() bool { return len(fl.all()) == 1 })
	got := fl.all()[0]
	if got.ID != id || got.Status != domain.StatusError || got.Error == nil || got.Error.Message != "boom" {
		t.Fatalf("reported %+v", got)
	}
}

func TestFailureHandlerIgnoresSuccess(t *testing.T) {
	fl := &failureLog{}
	e := newEnv(t, options{failures: fl, runner: success})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if n := len(fl.all()); n != 0 {
		t.Fatalf("reported %d failures for a success", n)
	}
}

func TestFailureHandlerHearsOfCrashes(t *testing.T) {
	fl := &failureLog{}
	e := newEnv(t, options{failures: fl, runner: success, attempts: 1})
	id := e.start(t)
	job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: job.ID, Attempt: job.Attempts}, e.clock.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fl.all(); len(got) != 1 || got[0].ID != id || got[0].Status != domain.StatusCrashed {
		t.Fatalf("reported %+v", got)
	}
}

// logBuffer is a bytes.Buffer safe for the worker's goroutines and the test.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

// record returns the first JSON log record with the message, or nil.
func (b *logBuffer) record(msg string) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range bytes.Split(b.buf.Bytes(), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(line, &rec) == nil && rec["msg"] == msg {
			return rec
		}
	}
	return nil
}

func TestTheWorkerLogsTheResultOfEachExecution(t *testing.T) {
	failed := func(_ context.Context, req engine.Request) (*engine.State, error) {
		return &engine.State{ExecutionID: req.ExecutionID, Status: domain.StatusError, RunData: map[string][]domain.NodeRun{"Start": {{}}},
			Error: &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "Start", Message: "boom"}}, nil
	}
	for _, tc := range []struct {
		name   string
		runner runnerFunc
		status domain.ExecutionStatus
		level  slog.Level
		extra  map[string]any
	}{
		{"success", success, domain.StatusSuccess, slog.LevelInfo, nil},
		{"error", failed, domain.StatusError, slog.LevelWarn, map[string]any{"errorNode": "Start", "error": "boom", "nodes": float64(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf logBuffer
			e := newEnv(t, options{runner: tc.runner, logger: slog.New(slog.NewJSONHandler(&buf, nil))})
			id := e.start(t)
			e.begin(t)
			e.waitStatus(t, id, tc.status)
			var rec map[string]any
			servicetest.Eventually(t, "the result log", func() bool { rec = buf.record("execution finished"); return rec != nil })
			want := map[string]any{
				"level": tc.level.String(), "execution": string(id), "workflow": string(e.wf.ID),
				"mode": string(domain.ModeManual), "status": string(tc.status), "worker": "wk_test",
			}
			for k, v := range tc.extra {
				want[k] = v
			}
			for k, v := range want {
				if rec[k] != v {
					t.Errorf("log %s = %v, want %v (record %v)", k, rec[k], v, rec)
				}
			}
			if _, ok := rec["durationMs"]; !ok {
				t.Errorf("log has no durationMs: %v", rec)
			}
		})
	}
}
