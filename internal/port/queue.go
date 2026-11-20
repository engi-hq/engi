package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// JobQueue is a durable at-least-once queue of execution jobs. Workers claim a
// job with a lease, extend it with heartbeats, and finish it with Complete,
// Fail or Release. Acting on a job whose lease was lost fails with an error
// wrapping domain.ErrLeaseLost.
type JobQueue interface {
	// Enqueue adds a queued job. A zero AvailableAt means now.
	Enqueue(ctx context.Context, j domain.Job) error
	// Claim leases the oldest available job to workerID, or returns an error
	// wrapping domain.ErrNoJob. It increments the job's attempts.
	Claim(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error)
	// Heartbeat extends the lease to now+lease.
	Heartbeat(ctx context.Context, id domain.JobID, workerID string, lease time.Duration) error
	// Complete removes a finished job.
	Complete(ctx context.Context, id domain.JobID, workerID string) error
	// Fail records reason and either requeues the job at retryAt or, when it
	// has used all its attempts, parks it as failed. It returns the new state.
	Fail(ctx context.Context, id domain.JobID, workerID, reason string, retryAt time.Time) (domain.Job, error)
	// Release gives a job back immediately without counting the attempt, for
	// example on graceful shutdown.
	Release(ctx context.Context, id domain.JobID, workerID string) error
}

// JobReclaimer recovers jobs of dead workers.
type JobReclaimer interface {
	// ReclaimExpired returns jobs whose lease expired to the queue, or parks
	// them as failed when attempts are exhausted. It returns the affected jobs
	// in their new state, with the worker that lost each lease, so the caller
	// can mark their executions crashed and tell an operator.
	ReclaimExpired(ctx context.Context) ([]domain.ReclaimedJob, error)
}

// JobDispatcher blocks until a job can be claimed.
type JobDispatcher interface {
	// Next waits for a job, claims it and returns it, or returns ctx.Err()
	// wrapped when ctx is done.
	Next(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error)
}

// QueueStats reports how much work waits in the queue, for backpressure.
type QueueStats interface {
	// Depth returns the number of jobs waiting to be claimed.
	Depth(ctx context.Context) (int, error)
}
