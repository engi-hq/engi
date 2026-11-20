package porttest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// QueueOptions are the settings a queue adapter must honour in the suite.
type QueueOptions struct {
	// MaxAttempts is how many claims a job gets before it is parked as failed.
	MaxAttempts int
	// PollInterval is the dispatcher's fallback poll; the suite sets it very
	// large to prove that Enqueue wakes waiting workers.
	PollInterval time.Duration
}

// QueueEnv is a queue plus the stores its jobs refer to.
type QueueEnv struct {
	Queue      port.JobQueue
	Reclaimer  port.JobReclaimer
	Dispatcher port.JobDispatcher
	// Stats is optional; the depth case is skipped without it.
	Stats      port.QueueStats
	Workflows  port.WorkflowStore
	Executions port.ExecutionStore
	Tx         port.TxManager
}

// QueueFactory builds a fresh, empty, migrated queue (sharing its database
// with the stores) that reads time from clk and registers its own cleanup.
type QueueFactory func(t *testing.T, clk port.Clock, opts QueueOptions) QueueEnv

type queueEnv struct {
	QueueEnv
	fx   *fixtures
	clk  *FakeClock
	wf   domain.Workflow
	opts QueueOptions
}

const (
	suiteLease    = time.Minute
	suiteWorker   = "worker-1"
	suiteMaxTries = 3
	suitePoll     = time.Hour
	waitLimit     = 5 * time.Second
)

// RunQueueSuite runs the job queue contract tests against queues from newQueue.
func RunQueueSuite(t *testing.T, newQueue QueueFactory) {
	t.Helper()
	cases := map[string]func(*testing.T, *queueEnv){
		"ClaimOrderAndEmpty":           testClaimOrder,
		"AvailableAtDelaysClaim":       testAvailableAt,
		"EnqueueNeedsExecution":        testEnqueueNeedsExecution,
		"ClaimIsExactlyOnceConcurrent": testConcurrentClaim,
		"CompleteRemovesJob":           testComplete,
		"LeaseExpiryRequeues":          testLeaseExpiry,
		"HeartbeatExtendsLease":        testHeartbeat,
		"LostLeaseRejectsOldOwner":     testLostLease,
		"FailRequeuesThenParks":        testFail,
		"ReclaimParksExhaustedJobs":    testReclaimExhausted,
		"ReleaseDoesNotCountAttempt":   testRelease,
		"NextWakesOnEnqueue":           testNextWakes,
		"NextStopsOnCancel":            testNextCancel,
		"EnqueueRollsBackWithTx":       testEnqueueTx,
		"PruningExecutionDropsItsJobs": testPruneDropsJobs,
		"DepthCountsQueuedJobs":        testDepth,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			clk := NewFakeClock()
			opts := QueueOptions{MaxAttempts: suiteMaxTries, PollInterval: suitePoll}
			env := &queueEnv{QueueEnv: newQueue(t, clk, opts), fx: newFixtures(t, clk), clk: clk, opts: opts}
			env.wf = env.fx.workflow("w")
			if _, err := env.Workflows.Save(context.Background(), env.wf); err != nil {
				t.Fatalf("seeding workflow: %v", err)
			}
			fn(t, env)
		})
	}
}

// enqueue creates an execution and a job for it, available after delay.
func (e *queueEnv) enqueue(t *testing.T, delay time.Duration) domain.Job {
	t.Helper()
	ctx := context.Background()
	x := e.fx.execution(e.wf.ID, 1)
	if err := e.Executions.Create(ctx, x); err != nil {
		t.Fatalf("creating execution: %v", err)
	}
	j := domain.Job{ID: e.fx.jobID(), ExecutionID: x.ID, AvailableAt: e.clk.Now().Add(delay)}
	if err := e.Queue.Enqueue(ctx, j); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return j
}

func (e *queueEnv) claim(t *testing.T, worker string) domain.Job {
	t.Helper()
	j, err := e.Queue.Claim(context.Background(), worker, suiteLease)
	if err != nil {
		t.Fatalf("Claim(%s): %v", worker, err)
	}
	return j
}

func (e *queueEnv) wantEmpty(t *testing.T) {
	t.Helper()
	if j, err := e.Queue.Claim(context.Background(), "probe", suiteLease); !errors.Is(err, domain.ErrNoJob) {
		t.Fatalf("Claim on empty queue = %+v, %v; want ErrNoJob", j, err)
	}
}

func testClaimOrder(t *testing.T, e *queueEnv) {
	e.wantEmpty(t)
	first := e.enqueue(t, 0)
	e.clk.Advance(time.Second)
	second := e.enqueue(t, 0)
	j := e.claim(t, suiteWorker)
	if j.ID != first.ID || j.Status != domain.JobLeased || j.Attempts != 1 ||
		j.LeaseOwner != suiteWorker || !j.LeaseUntil.Equal(e.clk.Now().Add(suiteLease)) || j.ExecutionID != first.ExecutionID {
		t.Errorf("first claim = %+v", j)
	}
	if j2 := e.claim(t, "worker-2"); j2.ID != second.ID {
		t.Errorf("second claim = %s, want %s", j2.ID, second.ID)
	}
	e.wantEmpty(t)
}

func testAvailableAt(t *testing.T, e *queueEnv) {
	later := e.enqueue(t, 10*time.Minute)
	e.wantEmpty(t)
	e.clk.Advance(9 * time.Minute)
	e.wantEmpty(t)
	e.clk.Advance(time.Minute)
	if j := e.claim(t, suiteWorker); j.ID != later.ID {
		t.Errorf("claimed %s, want %s", j.ID, later.ID)
	}
}

func testEnqueueNeedsExecution(t *testing.T, e *queueEnv) {
	err := e.Queue.Enqueue(context.Background(), domain.Job{ID: e.fx.jobID(), ExecutionID: e.fx.executionID()})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Enqueue for unknown execution = %v, want ErrNotFound", err)
	}
}

func testConcurrentClaim(t *testing.T, e *queueEnv) {
	const jobs, workers = 40, 8
	want := map[domain.JobID]bool{}
	for range jobs {
		want[e.enqueue(t, 0).ID] = true
	}
	var mu sync.Mutex
	claimed := map[domain.JobID]string{}
	var wg sync.WaitGroup
	for w := range workers {
		worker := "w" + string(rune('a'+w))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := e.Queue.Claim(context.Background(), worker, suiteLease)
				if errors.Is(err, domain.ErrNoJob) {
					return
				}
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				mu.Lock()
				if prev, dup := claimed[j.ID]; dup {
					t.Errorf("job %s claimed by %s and %s", j.ID, prev, worker)
				}
				claimed[j.ID] = worker
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(claimed) != jobs {
		t.Errorf("claimed %d jobs, want %d", len(claimed), jobs)
	}
	for id := range want {
		if _, ok := claimed[id]; !ok {
			t.Errorf("job %s never claimed", id)
		}
	}
}

func testComplete(t *testing.T, e *queueEnv) {
	e.enqueue(t, 0)
	j := e.claim(t, suiteWorker)
	if err := e.Queue.Complete(context.Background(), j.ID, "someone-else"); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("Complete by non-owner = %v, want ErrLeaseLost", err)
	}
	if err := e.Queue.Complete(context.Background(), j.ID, suiteWorker); err != nil {
		t.Fatal(err)
	}
	if err := e.Queue.Complete(context.Background(), j.ID, suiteWorker); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("second Complete = %v, want ErrLeaseLost", err)
	}
	e.clk.Advance(2 * suiteLease)
	if got, err := e.Reclaimer.ReclaimExpired(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("ReclaimExpired after Complete = %v, %v; want nothing", got, err)
	}
	e.wantEmpty(t)
}

func testLeaseExpiry(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	j := e.claim(t, suiteWorker)
	e.clk.Advance(suiteLease - time.Second)
	if got, err := e.Reclaimer.ReclaimExpired(ctx); err != nil || len(got) != 0 {
		t.Fatalf("early ReclaimExpired = %v, %v; want nothing", got, err)
	}
	e.wantEmpty(t)
	e.clk.Advance(2 * time.Second)
	got, err := e.Reclaimer.ReclaimExpired(ctx)
	if err != nil || len(got) != 1 || got[0].ID != j.ID || got[0].Status != domain.JobQueued || got[0].LeaseOwner != "" {
		t.Fatalf("ReclaimExpired = %+v, %v; want job requeued", got, err)
	}
	if got[0].PreviousOwner != suiteWorker || got[0].Attempts != 1 {
		t.Errorf("ReclaimExpired reported owner %q attempts %d, want %q and 1", got[0].PreviousOwner, got[0].Attempts, suiteWorker)
	}
	again := e.claim(t, "worker-2")
	if again.ID != j.ID || again.Attempts != 2 || again.LeaseOwner != "worker-2" {
		t.Errorf("reclaimed claim = %+v, want attempt 2 by worker-2", again)
	}
}

func testHeartbeat(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	j := e.claim(t, suiteWorker)
	for range 3 { // total time passed is well beyond one lease
		e.clk.Advance(suiteLease / 2)
		if err := e.Queue.Heartbeat(ctx, j.ID, suiteWorker, suiteLease); err != nil {
			t.Fatal(err)
		}
		if got, err := e.Reclaimer.ReclaimExpired(ctx); err != nil || len(got) != 0 {
			t.Fatalf("ReclaimExpired during heartbeats = %v, %v", got, err)
		}
	}
	if err := e.Queue.Heartbeat(ctx, j.ID, "intruder", suiteLease); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("Heartbeat by non-owner = %v, want ErrLeaseLost", err)
	}
	e.clk.Advance(2 * suiteLease)
	if got, err := e.Reclaimer.ReclaimExpired(ctx); err != nil || len(got) != 1 {
		t.Errorf("ReclaimExpired after heartbeats stop = %v, %v; want 1", got, err)
	}
}

func testLostLease(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	old := e.claim(t, suiteWorker)
	e.clk.Advance(2 * suiteLease)
	if _, err := e.Reclaimer.ReclaimExpired(ctx); err != nil {
		t.Fatal(err)
	}
	e.claim(t, "worker-2")
	if err := e.Queue.Heartbeat(ctx, old.ID, suiteWorker, suiteLease); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("stale Heartbeat = %v, want ErrLeaseLost", err)
	}
	if err := e.Queue.Complete(ctx, old.ID, suiteWorker); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("stale Complete = %v, want ErrLeaseLost", err)
	}
	if _, err := e.Queue.Fail(ctx, old.ID, suiteWorker, "x", e.clk.Now()); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("stale Fail = %v, want ErrLeaseLost", err)
	}
	if err := e.Queue.Release(ctx, old.ID, suiteWorker); !errors.Is(err, domain.ErrLeaseLost) {
		t.Errorf("stale Release = %v, want ErrLeaseLost", err)
	}
}

func testFail(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	retryDelay := 30 * time.Second
	for attempt := 1; attempt < e.opts.MaxAttempts; attempt++ {
		j := e.claim(t, suiteWorker)
		if j.Attempts != attempt {
			t.Fatalf("attempt = %d, want %d", j.Attempts, attempt)
		}
		got, err := e.Queue.Fail(ctx, j.ID, suiteWorker, "temporary", e.clk.Now().Add(retryDelay))
		if err != nil || got.Status != domain.JobQueued || got.LastError != "temporary" || got.LeaseOwner != "" {
			t.Fatalf("Fail = %+v, %v; want requeued with reason", got, err)
		}
		e.wantEmpty(t) // still backing off
		e.clk.Advance(retryDelay)
	}
	j := e.claim(t, suiteWorker)
	got, err := e.Queue.Fail(ctx, j.ID, suiteWorker, "fatal", e.clk.Now())
	if err != nil || got.Status != domain.JobFailed || got.Attempts != e.opts.MaxAttempts || got.LastError != "fatal" {
		t.Fatalf("final Fail = %+v, %v; want parked as failed", got, err)
	}
	e.clk.Advance(time.Hour)
	e.wantEmpty(t)
}

func testReclaimExhausted(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	for attempt := 1; attempt <= e.opts.MaxAttempts; attempt++ {
		e.claim(t, suiteWorker)
		e.clk.Advance(2 * suiteLease)
		got, err := e.Reclaimer.ReclaimExpired(ctx)
		if err != nil || len(got) != 1 {
			t.Fatalf("attempt %d: ReclaimExpired = %v, %v", attempt, got, err)
		}
		want := domain.JobQueued
		if attempt == e.opts.MaxAttempts {
			want = domain.JobFailed
		}
		if got[0].Status != want {
			t.Fatalf("attempt %d: status = %s, want %s", attempt, got[0].Status, want)
		}
		if got[0].PreviousOwner != suiteWorker || got[0].Attempts != attempt {
			t.Fatalf("attempt %d: reported owner %q attempts %d, want %q and %d", attempt, got[0].PreviousOwner, got[0].Attempts, suiteWorker, attempt)
		}
	}
	e.wantEmpty(t)
}

func testRelease(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	e.enqueue(t, 0)
	j := e.claim(t, suiteWorker)
	if err := e.Queue.Release(ctx, j.ID, suiteWorker); err != nil {
		t.Fatal(err)
	}
	again := e.claim(t, "worker-2")
	if again.ID != j.ID || again.Attempts != 1 {
		t.Errorf("after Release claim = %+v, want same job at attempt 1", again)
	}
}

func testNextWakes(t *testing.T, e *queueEnv) {
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	type result struct {
		job domain.Job
		err error
	}
	out := make(chan result, 1)
	go func() {
		j, err := e.Dispatcher.Next(ctx, suiteWorker, suiteLease)
		out <- result{j, err}
	}()
	select {
	case r := <-out:
		t.Fatalf("Next returned on an empty queue: %+v, %v", r.job, r.err)
	case <-time.After(100 * time.Millisecond):
	}
	want := e.enqueue(t, 0) // the poll interval is an hour: only the wakeup can deliver this in time
	select {
	case r := <-out:
		if r.err != nil || r.job.ID != want.ID {
			t.Errorf("Next = %+v, %v; want job %s", r.job, r.err, want.ID)
		}
	case <-ctx.Done():
		t.Fatal("Next was not woken by Enqueue")
	}
}

func testNextCancel(t *testing.T, e *queueEnv) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := e.Dispatcher.Next(ctx, suiteWorker, suiteLease)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Next = %v, want context.Canceled", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("Next ignored cancellation")
	}
}

func testEnqueueTx(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	x := e.fx.execution(e.wf.ID, 1)
	boom := errors.New("boom")
	err := e.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := e.Executions.Create(ctx, x); err != nil {
			return err
		}
		if err := e.Queue.Enqueue(ctx, domain.Job{ID: e.fx.jobID(), ExecutionID: x.ID}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithinTx = %v, want boom", err)
	}
	e.wantEmpty(t)
	if _, err := e.Executions.Get(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("execution survived rollback: %v", err)
	}
	err = e.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := e.Executions.Create(ctx, x); err != nil {
			return err
		}
		return e.Queue.Enqueue(ctx, domain.Job{ID: e.fx.jobID(), ExecutionID: x.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if j := e.claim(t, suiteWorker); j.ExecutionID != x.ID {
		t.Errorf("claimed execution %s, want %s", j.ExecutionID, x.ID)
	}
}

func testPruneDropsJobs(t *testing.T, e *queueEnv) {
	ctx := context.Background()
	j := e.enqueue(t, time.Hour)
	x, err := e.Executions.Get(ctx, j.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	fin := e.clk.Now()
	x.Status, x.FinishedAt = domain.StatusSuccess, &fin
	if err := e.Executions.Update(ctx, x); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(48 * time.Hour)
	n, err := e.Executions.Prune(ctx, map[domain.ExecutionStatus]time.Time{domain.StatusSuccess: e.clk.Now()})
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
	e.wantEmpty(t)
}

func testDepth(t *testing.T, e *queueEnv) {
	if e.Stats == nil {
		t.Skip("queue has no depth statistics")
	}
	ctx := context.Background()
	depth := func(want int) {
		t.Helper()
		if got, err := e.Stats.Depth(ctx); err != nil || got != want {
			t.Fatalf("Depth = %d, %v; want %d", got, err, want)
		}
	}
	depth(0)
	e.enqueue(t, 0)
	e.enqueue(t, 0)
	depth(2)
	e.claim(t, suiteWorker)
	depth(1)
}
