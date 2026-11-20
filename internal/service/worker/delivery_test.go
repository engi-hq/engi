package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/worker"
)

// These tests cover delivery when a worker dies after it stored its result
// but before the queue heard about it (the ack was lost), and when it dies in
// the middle of a run. The rules under test:
//
//   - a job whose execution already ended is acked without running anything;
//   - a job whose execution is waiting is stale and must not resume it (the
//     waiter or a resume token queues a fresh job when the wait is over);
//   - a job whose worker died mid-run runs again from its last stored
//     checkpoint: the saved input, or the saved waiting state of a resumed run.

// jobPort is what a worker needs from the queue.
type jobPort interface {
	port.JobQueue
	port.JobDispatcher
	port.JobReclaimer
}

// ackLostQueue is the queue as seen by a worker that dies after storing its
// result: claims and heartbeats still pass, but Complete and Release never
// reach the queue.
type ackLostQueue struct {
	*servicetest.Queue
	acksLost atomic.Int64
}

func (q *ackLostQueue) Complete(context.Context, domain.JobID, string) error {
	q.acksLost.Add(1)
	return errors.New("connection to the queue lost before the ack")
}

func (q *ackLostQueue) Release(context.Context, domain.JobID, string) error {
	q.acksLost.Add(1)
	return errors.New("connection to the queue lost before the release")
}

// workerAs builds a worker with its own id on queue q, sharing the env's
// stores, clock, bus and configuration.
func (e *env) workerAs(t *testing.T, id string, q jobPort, runner runnerFunc) *worker.Worker {
	t.Helper()
	w, err := worker.New(e.wcfg, e.qcfg, worker.Deps{
		WorkerID: id, Dispatcher: q, Queue: q, Reclaimer: q, Executions: e.execs, States: e.execs,
		Versions: e.store, Tx: e.store, Bus: e.bus, Clock: e.clock, Engine: runner, Codec: servicetest.Codec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// dieBeforeAck runs the queued job on a worker whose acks are lost, and returns
// once that worker has stopped. The execution is stored, the job stays leased
// by the dead worker.
func (e *env) dieBeforeAck(t *testing.T, runner runnerFunc) {
	t.Helper()
	q := &ackLostQueue{Queue: e.queue}
	w := e.workerAs(t, "wk_dying", q, runner)
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	servicetest.Eventually(t, "the worker to lose its ack", func() bool { return q.acksLost.Load() > 0 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// recoverAfterLease moves the clock past the dead worker's lease, reclaims its
// job, and starts a healthy worker on the queue.
func (e *env) recoverAfterLease(t *testing.T, runner runnerFunc) {
	t.Helper()
	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := e.workerAs(t, "wk_healthy", e.queue, runner)
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.Shutdown(ctx)
	})
}

// counted wraps a runner so the test can tell how often the engine was called.
func counted(calls *atomic.Int64, inner runnerFunc) runnerFunc {
	return func(ctx context.Context, req engine.Request) (*engine.State, error) {
		calls.Add(1)
		return inner(ctx, req)
	}
}

func finishedRun(status domain.ExecutionStatus) runnerFunc {
	return func(_ context.Context, req engine.Request) (*engine.State, error) {
		return &engine.State{
			ExecutionID: req.ExecutionID, Status: status, FinishedAt: time.Date(2026, 5, 6, 8, 0, 0, 0, time.UTC),
			RunData: map[string][]domain.NodeRun{"Start": {{RunIndex: 0, Status: domain.NodeRunSuccess}}},
			Error:   errorFor(status),
		}, nil
	}
}

func errorFor(status domain.ExecutionStatus) *domain.ErrorInfo {
	switch status {
	case domain.StatusError:
		return &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: "boom"}
	case domain.StatusCanceled:
		return &domain.ErrorInfo{Kind: domain.ErrorKindCanceled, Message: "execution canceled"}
	}
	return nil
}

func TestAckLostAfterFinishDoesNotRunAgain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.ExecutionStatus
	}{
		{"success", domain.StatusSuccess},
		{"error", domain.StatusError},
		{"canceled", domain.StatusCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			runner := counted(&calls, finishedRun(tc.status))
			e := newEnv(t, options{runner: runner})
			id := e.start(t)

			e.dieBeforeAck(t, runner)
			e.waitStatus(t, id, tc.status)
			before, _ := e.execs.Get(context.Background(), id)
			if len(e.queue.Jobs()) != 1 {
				t.Fatalf("the unacked job must still be in the queue: %+v", e.queue.Jobs())
			}

			e.recoverAfterLease(t, runner)
			servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

			after, _ := e.execs.Get(context.Background(), id)
			if calls.Load() != 1 {
				t.Fatalf("the execution ran %d times, want 1", calls.Load())
			}
			if after.Status != tc.status || after.FinishedAt == nil || !after.FinishedAt.Equal(*before.FinishedAt) {
				t.Fatalf("status changed: before %+v, after %+v", before, after)
			}
			if fin := e.bus.Events(port.EventExecutionFinished); len(fin) != 1 {
				t.Fatalf("finish events = %d, want 1", len(fin))
			}
		})
	}
}

func TestCrashedExecutionIsNotRunAgain(t *testing.T) {
	var calls atomic.Int64
	runner := counted(&calls, success)
	e := newEnv(t, options{runner: runner})
	id := e.start(t)

	// The reclaimer marked the execution crashed while its job was still
	// leased by the dead worker.
	if _, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute); err != nil {
		t.Fatal(err)
	}
	ex, _ := e.execs.Get(context.Background(), id)
	now := e.clock.Now()
	ex.Status, ex.FinishedAt = domain.StatusCrashed, &now
	e.execs.SetExecution(ex)

	e.recoverAfterLease(t, runner)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 0 || e.status(id) != domain.StatusCrashed {
		t.Fatalf("calls = %d, status = %s; a crashed execution must not run", calls.Load(), e.status(id))
	}
}

func TestAckLostWhileWaitingDoesNotResumeEarly(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wait is the time wait relative to the clock; zero with a token means a token wait.
		wait  time.Duration
		timed bool
		token string
	}{
		{name: "time wait not due yet", wait: time.Hour, timed: true},
		{name: "time wait already due", wait: -time.Hour, timed: true},
		{name: "token wait", token: "tok_secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			var until time.Time
			waiting := func(_ context.Context, req engine.Request) (*engine.State, error) {
				return &engine.State{
					ExecutionID: req.ExecutionID, Status: domain.StatusWaiting,
					RunData: map[string][]domain.NodeRun{"Start": {{Status: domain.NodeRunWaiting}}},
					Pending: &engine.PendingWait{Node: "Start", Until: until, ResumeToken: tc.token},
				}, nil
			}
			e := newEnv(t, options{runner: counted(&calls, waiting)})
			if tc.timed {
				until = e.clock.Now().Add(tc.wait)
			}
			id := e.start(t)

			e.dieBeforeAck(t, counted(&calls, waiting))
			e.waitStatus(t, id, domain.StatusWaiting)

			e.recoverAfterLease(t, counted(&calls, success))
			servicetest.Eventually(t, "the stale job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

			if calls.Load() != 1 {
				t.Fatalf("the engine ran %d times, want 1 (only the first run); the stale job must not resume the wait", calls.Load())
			}
			if e.status(id) != domain.StatusWaiting || !e.execs.HasState(id) {
				t.Fatalf("status = %s, saved state kept = %v; the waiter or the token must resume it", e.status(id), e.execs.HasState(id))
			}
			if fin := e.bus.Events(port.EventExecutionFinished); len(fin) != 1 || fin[0].Status != domain.StatusWaiting {
				t.Fatalf("finish events = %+v", fin)
			}
		})
	}
}

func TestDeadMidRunReRunsFromTheSavedStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		// saved is the waiting state a resumed run started from; nil means the
		// run started fresh from its input.
		saved *engine.State
	}{
		{name: "fresh run"},
		{name: "resumed run", saved: &engine.State{
			Status: domain.StatusWaiting, Pending: &engine.PendingWait{Node: "Start", Resumed: true},
			RunData: map[string][]domain.NodeRun{"Start": {{Status: domain.NodeRunWaiting}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			var got engine.Request
			runner := func(ctx context.Context, req engine.Request) (*engine.State, error) {
				got = req
				return counted(&calls, success)(ctx, req)
			}
			e := newEnv(t, options{runner: runner})
			id := e.start(t)
			if tc.saved != nil {
				blob, err := servicetest.Codec{}.EncodeState(&engine.State{ExecutionID: id, Status: tc.saved.Status,
					Pending: tc.saved.Pending, RunData: tc.saved.RunData})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.execs.SaveState(context.Background(), id, blob); err != nil {
					t.Fatal(err)
				}
			}
			// The worker died halfway: status running, two nodes recorded.
			job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: job.ID, Attempt: job.Attempts}, e.clock.Now()); err != nil || !ok {
				t.Fatalf("claim = %v, %v", ok, err)
			}
			ex, _ := e.execs.Get(context.Background(), id)
			ex.RunData = map[string][]domain.NodeRun{
				"Start": {{RunIndex: 0, Status: domain.NodeRunSuccess}},
				"Next":  {{RunIndex: 0, Status: domain.NodeRunSuccess}},
			}
			e.execs.SetExecution(ex)

			e.clock.Advance(2 * time.Minute)
			if err := e.worker.ReclaimOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.status(id) != domain.StatusQueued {
				t.Fatalf("status after reclaim = %s, want queued", e.status(id))
			}
			e.recoverAfterLease(t, runner)
			e.waitStatus(t, id, domain.StatusSuccess)
			servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

			if calls.Load() != 1 {
				t.Fatalf("the engine ran %d times for the reclaimed job, want 1", calls.Load())
			}
			if tc.saved == nil {
				if got.Resume != nil || got.StartNode != "Start" || len(got.StartItems) != 1 {
					t.Fatalf("fresh run must restart from its saved input: %+v", got)
				}
			} else if got.Resume == nil || got.Resume.Pending == nil || got.Resume.Pending.Node != "Start" || got.StartNode != "" {
				t.Fatalf("resumed run must restart from its saved wait: %+v", got)
			}
			done, _ := e.execs.Get(context.Background(), id)
			if _, stale := done.RunData["Next"]; stale || done.RunData["Start"][0].Status != domain.NodeRunSuccess {
				t.Fatalf("the interrupted run's data must be replaced by the new run: %+v", done.RunData)
			}
		})
	}
}
