package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

// These tests cover the claim of a run: only the job that moves an execution
// from queued to running may run the engine. A second job for the same
// execution (a resume that queued one while the stale job still held its
// lease) is a duplicate and is dropped.

// gatedRunner runs successfully once release is closed. It counts every call.
func gatedRunner(calls *atomic.Int64, release <-chan struct{}) runnerFunc {
	return func(ctx context.Context, req engine.Request) (*engine.State, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return success(ctx, req)
	}
}

func TestTwoJobsForOneExecutionRunItOnce(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	e := newEnv(t, options{runner: gatedRunner(&calls, release), concurrency: 2})
	id := e.start(t)

	// A resume queued a second job for the same execution.
	if err := e.queue.Enqueue(context.Background(), domain.Job{ID: "jb_duplicate", ExecutionID: id}); err != nil {
		t.Fatal(err)
	}
	e.begin(t)

	// The loser completes its job while the winner is still inside the engine.
	servicetest.Eventually(t, "the duplicate job to be dropped", func() bool { return len(e.queue.Jobs()) == 1 })
	if calls.Load() != 1 {
		t.Fatalf("engine calls while the winner runs = %d, want 1", calls.Load())
	}
	if got := e.status(id); got != domain.StatusRunning {
		t.Fatalf("status = %s, want running under the winning job", got)
	}

	close(release)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the winning job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 1 {
		t.Fatalf("the execution ran %d times, want exactly 1", calls.Load())
	}
	if ex, _ := e.execs.Get(context.Background(), id); ex.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (the duplicate must not count)", ex.Attempts)
	}
}

// A job whose lease expired while another job drives the execution must not
// reset that run to queued, or the stale job would run it a second time.
func TestStaleJobReclaimDoesNotResetAnotherJobsRun(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	e := newEnv(t, options{runner: gatedRunner(&calls, release), concurrency: 2})
	id := e.start(t)

	// The first worker claimed the job and died before it started the run.
	if _, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute); err != nil {
		t.Fatal(err)
	}
	// A resume queued a fresh job for the same execution, and a healthy worker runs it.
	fresh := domain.Job{ID: "jb_fresh", ExecutionID: id}
	if err := e.queue.Enqueue(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	e.begin(t)
	servicetest.Eventually(t, "the fresh run to start", func() bool { return e.status(id) == domain.StatusRunning && calls.Load() == 1 })

	// The stale lease expires while the fresh run is still going. The fresh
	// job's heartbeat keeps its own lease alive, so only the stale one is reclaimed.
	e.clock.Advance(2 * time.Minute)
	servicetest.Eventually(t, "the fresh job's lease to be renewed", func() bool {
		for _, j := range e.queue.Jobs() {
			if j.ID == fresh.ID && j.LeaseUntil.After(e.clock.Now()) {
				return true
			}
		}
		return false
	})
	if err := e.worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	servicetest.Eventually(t, "the stale job to be dropped", func() bool { return len(e.queue.Jobs()) == 1 })

	if got := e.status(id); got != domain.StatusRunning {
		t.Fatalf("status after the stale reclaim = %s, want running (the fresh job still holds it)", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("engine calls = %d, want 1", calls.Load())
	}
	if ex, _ := e.execs.Get(context.Background(), id); ex.LastRecoveredAt != nil {
		t.Fatalf("a stale job must not record a recovery: %v", ex.LastRecoveredAt)
	}

	close(release)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the fresh job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 1 {
		t.Fatalf("the execution ran %d times, want exactly 1", calls.Load())
	}
}

// A reclaimed job that is claimed before its execution was requeued takes the
// run over from the dead worker. That is one recovery and one more attempt.
func TestReclaimedJobTakesOverItsRun(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, options{runner: counted(&calls, success)})
	id := e.start(t)

	job, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: job.ID, Attempt: job.Attempts}, e.clock.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	// The queue reclaims the job, but the requeue of the execution has not landed yet.
	e.clock.Advance(2 * time.Minute)
	if _, err := e.queue.ReclaimExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	reclaimedAt := e.clock.Now()

	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })

	if calls.Load() != 1 {
		t.Fatalf("the engine ran %d times, want 1", calls.Load())
	}
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the dead start and the takeover)", ex.Attempts)
	}
	if ex.LastRecoveredAt == nil || !ex.LastRecoveredAt.Equal(reclaimedAt) {
		t.Fatalf("last recovered at = %v, want %v", ex.LastRecoveredAt, reclaimedAt)
	}
}

// A run that a dead worker lost and the reclaim requeued is recorded as a
// recovery, and its next start counts as a second attempt.
func TestReclaimRecordsRecoveryAndAttempts(t *testing.T) {
	var calls atomic.Int64
	runner := counted(&calls, success)
	e := newEnv(t, options{runner: runner})
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
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Status != domain.StatusQueued || ex.LastRecoveredAt == nil || ex.Attempts != 1 {
		t.Fatalf("after reclaim: status %s, recovered %v, attempts %d; want queued, recovered, 1 attempt",
			ex.Status, ex.LastRecoveredAt, ex.Attempts)
	}

	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	done, _ := e.execs.Get(context.Background(), id)
	if done.Attempts != 2 || done.LastRecoveredAt == nil {
		t.Fatalf("finished: attempts %d, recovered %v; want 2 attempts and a recovery", done.Attempts, done.LastRecoveredAt)
	}
}

// A normal run counts one attempt and no recovery.
func TestFreshRunCountsOneAttempt(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, options{runner: counted(&calls, success)})
	id := e.start(t)
	e.begin(t)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	ex, _ := e.execs.Get(context.Background(), id)
	if ex.Attempts != 1 || ex.LastRecoveredAt != nil {
		t.Fatalf("attempts %d, recovered %v; want 1 and none", ex.Attempts, ex.LastRecoveredAt)
	}
}

// The reclaim requeues the run after the queue reclaimed the job. If the job
// was claimed again before that requeue lands, the requeue comes from the
// earlier claim and must not reset the run the new claim drives.
func TestLateRequeueFromAnEarlierClaimIsIgnored(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	e := newEnv(t, options{runner: gatedRunner(&calls, release)})
	id := e.start(t)

	dead, err := e.queue.Claim(context.Background(), "wk_dead", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.execs.ClaimRun(context.Background(), id, domain.RunHolder{JobID: dead.ID, Attempt: dead.Attempts}, e.clock.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	e.clock.Advance(2 * time.Minute)
	if _, err := e.queue.ReclaimExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The job is claimed again (attempt 2) and takes the run over before the reclaim requeues it.
	e.begin(t)
	servicetest.Eventually(t, "the takeover", func() bool { return e.status(id) == domain.StatusRunning && calls.Load() == 1 })

	now := e.clock.Now()
	requeued, err := e.execs.RequeueRun(context.Background(), id, domain.RunHolder{JobID: dead.ID, Attempt: dead.Attempts}, &now)
	if err != nil || requeued {
		t.Fatalf("late requeue from the earlier claim = %v, %v; want ignored", requeued, err)
	}
	if got := e.status(id); got != domain.StatusRunning {
		t.Fatalf("status = %s, want running under the takeover", got)
	}
	close(release)
	e.waitStatus(t, id, domain.StatusSuccess)
	servicetest.Eventually(t, "the job to complete", func() bool { return len(e.queue.Jobs()) == 0 })
	if calls.Load() != 1 {
		t.Fatalf("the execution ran %d times, want 1", calls.Load())
	}
}
