package worker_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

// aliveQueue is a queue whose heartbeats always succeed, so a zombie worker
// never notices that its lease was lost. It counts the completes it sends.
type aliveQueue struct {
	*servicetest.Queue
	completes atomic.Int64
}

func (q *aliveQueue) Heartbeat(context.Context, domain.JobID, string, time.Duration) error {
	return nil
}

func (q *aliveQueue) Complete(ctx context.Context, id domain.JobID, workerID string) error {
	q.completes.Add(1)
	return q.Queue.Complete(ctx, id, workerID) //nolint:wrapcheck // the test asserts on the error
}

// A zombie whose lease was reclaimed and taken over by a newer claim must not
// store its result, publish its finish event, or start the error workflow.
func TestZombieResultAfterTakeoverIsDropped(t *testing.T) {
	e := newEnv(t, options{runner: success, concurrency: 1})
	id := e.start(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	zombieRun := func(ctx context.Context, req engine.Request) (*engine.State, error) {
		close(entered)
		<-release
		return finishedRun(domain.StatusError)(ctx, req)
	}
	zq := &aliveQueue{Queue: e.queue}
	zombie := e.workerAs(t, "wk_zombie", zq, zombieRun)
	if err := zombie.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = zombie.Shutdown(ctx)
	})
	<-entered

	// The zombie's lease runs out; the reclaim requeues the job and the run.
	e.clock.Advance(2 * time.Minute)
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A healthy worker takes the run over and finishes it successfully.
	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the healthy job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

	// The zombie wakes up with an error result and tries to store it.
	releaseOnce.Do(func() { close(release) })
	servicetest.Eventually(t, "the zombie to drop its job", func() bool { return zq.completes.Load() == 1 })

	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Status != domain.StatusSuccess || ex.Error != nil {
		t.Fatalf("stored result = %s (error %+v), want the new holder's success", ex.Status, ex.Error)
	}
	if ex.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", ex.Attempts)
	}
	fin := e.bus.Events(port.EventExecutionFinished)
	if len(fin) != 1 || fin[0].Status != domain.StatusSuccess {
		t.Fatalf("finish events = %+v, want exactly one success", fin)
	}
}

// A crash ordered by an exhausted job must do nothing once another claim holds
// the execution: the running job keeps it and finishes it.
func TestCrashAfterNewClaimDoesNothing(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	e := newEnv(t, options{runner: gatedRunner(&calls, release), concurrency: 2, attempts: 1})
	id := e.start(t)

	// The first job's worker dies; its job has no attempts left.
	if _, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute); err != nil {
		t.Fatal(err)
	}
	// A duplicate job for the same execution is claimed and starts the run.
	if err := e.queue.Enqueue(context.Background(), domain.Job{ID: "jb_duplicate", ExecutionID: id}); err != nil {
		t.Fatal(err)
	}
	e.begin(t)
	servicetest.Eventually(t, "the duplicate to start the run", func() bool { return e.status(id) == domain.StatusRunning && calls.Load() == 1 })

	// Keep the duplicate's lease alive, then reclaim the dead job: it is parked
	// failed and the reclaimer tries to mark the execution crashed.
	e.clock.Advance(2 * time.Minute)
	servicetest.Eventually(t, "the duplicate's lease to be renewed", func() bool {
		for _, j := range e.queue.Jobs() {
			if j.ID == "jb_duplicate" && j.LeaseUntil.After(e.clock.Now()) {
				return true
			}
		}
		return false
	})
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.status(id); got != domain.StatusRunning {
		t.Fatalf("status after the crash attempt = %s, want running under the new claim", got)
	}
	if fin := e.bus.Events(port.EventExecutionFinished); len(fin) != 0 {
		t.Fatalf("a refused crash must publish nothing, got %+v", fin)
	}

	close(release)
	e.waitStatus(t, id, domain.StatusSuccess)
	// The exhausted job stays parked as failed; only the duplicate must complete.
	servicetest.Eventually(t, "the duplicate job to complete", func() bool {
		for _, j := range e.queue.Jobs() {
			if j.ID == "jb_duplicate" {
				return false
			}
		}
		return true
	})
	if calls.Load() != 1 {
		t.Fatalf("the execution ran %d times, want 1", calls.Load())
	}
}
