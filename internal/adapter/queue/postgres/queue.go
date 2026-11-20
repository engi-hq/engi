// Package postgres implements the durable job queue on the Postgres jobs table.
//
// Claiming is one UPDATE ... RETURNING on the oldest available job picked with
// FOR UPDATE SKIP LOCKED, so concurrent workers in any number of pods never
// claim the same job and never block each other. Enqueue and requeues send a
// NOTIFY, which wakes the dispatchers of every pod; the poll interval is only
// the fallback for a missed notification or a dropped listener connection.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// notifyChannel carries "a job became claimable" wake-ups between pods.
const notifyChannel = "engi_jobs"

const jobColumns = `id, execution_id, status, attempts, available_at, lease_owner, lease_until, last_error, created_at`

// Queue implements port.JobQueue, port.JobReclaimer and port.JobDispatcher.
type Queue struct {
	run *pgdb.Runner
	cfg config.QueueConfig
	clk port.Clock

	listener *pgdb.Listener

	mu   sync.Mutex
	wake chan struct{} // closed and replaced to wake every waiter
}

var (
	_ port.JobQueue      = (*Queue)(nil)
	_ port.JobReclaimer  = (*Queue)(nil)
	_ port.JobDispatcher = (*Queue)(nil)
	_ port.QueueStats    = (*Queue)(nil)
)

// New returns a Queue over a migrated database opened with pgdb.Target.Open.
// Without Listen it still works: dispatchers then rely on the poll interval to
// notice jobs enqueued by other pods.
func New(db *sql.DB, cfg config.QueueConfig, clk port.Clock) *Queue {
	return &Queue{run: pgdb.NewRunner(db), cfg: cfg, clk: clk, wake: make(chan struct{})}
}

// Listen starts the LISTEN connection that wakes dispatchers when any pod
// enqueues a job, retrying after reconnectDelay when the connection drops.
// Close stops it.
func (q *Queue) Listen(ctx context.Context, t *pgdb.Target, reconnectDelay time.Duration, log *slog.Logger) error {
	q.listener = pgdb.NewListener(t, []string{notifyChannel}, reconnectDelay, log,
		func(string, string) { q.notify() }, q.notify) // a reconnect may have missed wake-ups
	if err := q.listener.Start(ctx); err != nil {
		return fmt.Errorf("starting queue listener: %w", err)
	}
	return nil
}

// Close stops the listener, if any. It does not close the database.
func (q *Queue) Close() {
	if q.listener != nil {
		q.listener.Stop()
	}
}

// signal asks every pod's dispatchers to look for work. Inside a transaction
// the notification is delivered at commit, so a rolled-back enqueue wakes
// nobody. A failure only delays pods until their next poll, so it is ignored.
func (q *Queue) signal(ctx context.Context) {
	_, _ = q.run.Q(ctx).ExecContext(ctx, `SELECT pg_notify(?, '')`, notifyChannel)
	q.notify()
}

// notify wakes every goroutine blocked in Next.
func (q *Queue) notify() {
	q.mu.Lock()
	defer q.mu.Unlock()
	close(q.wake)
	q.wake = make(chan struct{})
}

func (q *Queue) waitChan() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.wake
}

// Depth implements port.QueueStats: the jobs waiting to be claimed, including
// those not yet due.
func (q *Queue) Depth(ctx context.Context) (int, error) {
	var n int
	err := q.run.Q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status = ?`, string(domain.JobQueued)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting queued jobs: %w", err)
	}
	return n, nil
}

// Enqueue implements port.JobQueue.
func (q *Queue) Enqueue(ctx context.Context, j domain.Job) error {
	if j.ID == "" || j.ExecutionID == "" {
		return errors.New("enqueueing job: id and execution id are required")
	}
	now := q.clk.Now()
	avail := j.AvailableAt
	if avail.IsZero() {
		avail = now
	}
	_, err := q.run.Q(ctx).ExecContext(ctx,
		`INSERT INTO jobs (id, execution_id, status, attempts, available_at, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?, ?)`,
		string(j.ID), string(j.ExecutionID), string(domain.JobQueued),
		pgdb.Stamp(avail), pgdb.Stamp(now), pgdb.Stamp(now))
	switch {
	case pgdb.IsForeignKeyViolation(err):
		return fmt.Errorf("enqueueing job %s: execution %s: %w", j.ID, j.ExecutionID, domain.ErrNotFound)
	case pgdb.IsPrimaryKeyViolation(err):
		return fmt.Errorf("enqueueing job %s: %w", j.ID, domain.ErrAlreadyExists)
	case err != nil:
		return fmt.Errorf("enqueueing job %s: %w", j.ID, err)
	}
	q.signal(ctx)
	return nil
}

// Claim implements port.JobQueue.
func (q *Queue) Claim(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	now := q.clk.Now()
	var job domain.Job
	err := q.run.Do(ctx, func(db pgdb.DBTX) error {
		row := db.QueryRowContext(ctx,
			`UPDATE jobs SET status = ?, lease_owner = ?, lease_until = ?, attempts = attempts + 1, updated_at = ?
			 WHERE id = (SELECT id FROM jobs WHERE status = ? AND available_at <= ? ORDER BY available_at, id LIMIT 1
			                FOR UPDATE SKIP LOCKED)
			 RETURNING `+jobColumns,
			string(domain.JobLeased), workerID, pgdb.Stamp(now.Add(lease)), pgdb.Stamp(now),
			string(domain.JobQueued), pgdb.Stamp(now))
		var err error
		job, err = scanJob(row)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, domain.ErrNoJob
	}
	if err != nil {
		return domain.Job{}, fmt.Errorf("claiming job: %w", err)
	}
	return job, nil
}

// Heartbeat implements port.JobQueue.
func (q *Queue) Heartbeat(ctx context.Context, id domain.JobID, workerID string, lease time.Duration) error {
	now := q.clk.Now()
	res, err := q.run.Q(ctx).ExecContext(ctx,
		`UPDATE jobs SET lease_until = ?, updated_at = ? WHERE id = ? AND lease_owner = ? AND status = ?`,
		pgdb.Stamp(now.Add(lease)), pgdb.Stamp(now), string(id), workerID, string(domain.JobLeased))
	return leaseResult(res, err, id, "heartbeat")
}

// Complete implements port.JobQueue.
func (q *Queue) Complete(ctx context.Context, id domain.JobID, workerID string) error {
	res, err := q.run.Q(ctx).ExecContext(ctx,
		`DELETE FROM jobs WHERE id = ? AND lease_owner = ? AND status = ?`,
		string(id), workerID, string(domain.JobLeased))
	return leaseResult(res, err, id, "complete")
}

// Fail implements port.JobQueue.
func (q *Queue) Fail(ctx context.Context, id domain.JobID, workerID, reason string, retryAt time.Time) (domain.Job, error) {
	now := q.clk.Now()
	job, err := q.updateOwned(ctx, id, workerID,
		`UPDATE jobs SET status = CASE WHEN attempts >= ? THEN ? ELSE ? END,
			available_at = ?, lease_owner = NULL, lease_until = NULL, last_error = ?, updated_at = ?
		 WHERE id = ? AND lease_owner = ? AND status = ?
		 RETURNING `+jobColumns,
		q.cfg.MaxAttempts, string(domain.JobFailed), string(domain.JobQueued),
		pgdb.Stamp(retryAt), reason, pgdb.Stamp(now), string(id), workerID, string(domain.JobLeased))
	if err != nil {
		return domain.Job{}, fmt.Errorf("failing job %s: %w", id, err)
	}
	if job.Status == domain.JobQueued {
		q.signal(ctx)
	}
	return job, nil
}

// Release implements port.JobQueue.
func (q *Queue) Release(ctx context.Context, id domain.JobID, workerID string) error {
	_, err := q.updateOwned(ctx, id, workerID,
		`UPDATE jobs SET status = ?, attempts = GREATEST(attempts - 1, 0), lease_owner = NULL, lease_until = NULL, updated_at = ?
		 WHERE id = ? AND lease_owner = ? AND status = ?
		 RETURNING `+jobColumns,
		string(domain.JobQueued), pgdb.Stamp(q.clk.Now()), string(id), workerID, string(domain.JobLeased))
	if err != nil {
		return fmt.Errorf("releasing job %s: %w", id, err)
	}
	q.signal(ctx)
	return nil
}

// updateOwned runs an UPDATE ... RETURNING guarded by lease ownership.
func (q *Queue) updateOwned(ctx context.Context, id domain.JobID, workerID, query string, args ...any) (domain.Job, error) {
	row := q.run.Q(ctx).QueryRowContext(ctx, query, args...)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, fmt.Errorf("worker %q does not own job %s: %w", workerID, id, domain.ErrLeaseLost)
	}
	return job, err
}

// ReclaimExpired implements port.JobReclaimer.
func (q *Queue) ReclaimExpired(ctx context.Context) ([]domain.ReclaimedJob, error) {
	now := pgdb.Stamp(q.clk.Now())
	var out []domain.ReclaimedJob
	err := q.run.Do(ctx, func(db pgdb.DBTX) error {
		out = out[:0]
		// The expired rows are locked in a subquery so their previous owner can
		// be returned next to the new state, without racing another reclaimer.
		rows, err := db.QueryContext(ctx,
			`UPDATE jobs SET status = CASE WHEN attempts >= ? THEN ? ELSE ? END,
				available_at = ?, lease_owner = NULL, lease_until = NULL, last_error = ?, updated_at = ?
			 FROM (SELECT id AS expired_id, lease_owner AS previous_owner FROM jobs
			       WHERE status = ? AND lease_until < ? FOR UPDATE) AS expired
			 WHERE jobs.id = expired.expired_id
			 RETURNING `+prefixed("jobs", jobColumns)+`, expired.previous_owner`,
			q.cfg.MaxAttempts, string(domain.JobFailed), string(domain.JobQueued),
			now, "lease expired", now, string(domain.JobLeased), now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var prev sql.NullString
			j, err := scanJob(rows, &prev)
			if err != nil {
				return err
			}
			out = append(out, domain.ReclaimedJob{Job: j, PreviousOwner: prev.String})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reclaiming expired jobs: %w", err)
	}
	for _, j := range out {
		if j.Status == domain.JobQueued {
			q.signal(ctx)
			break
		}
	}
	return out, nil
}

// Next implements port.JobDispatcher. It claims immediately when a job is
// available, otherwise sleeps until Enqueue wakes it, the poll interval
// passes, or ctx is done.
func (q *Queue) Next(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	timer := time.NewTimer(q.cfg.PollInterval)
	defer timer.Stop()
	for {
		// Grab the wake channel before claiming so a concurrent Enqueue
		// between the claim and the wait cannot be missed.
		wake := q.waitChan()
		job, err := q.Claim(ctx, workerID, lease)
		if !errors.Is(err, domain.ErrNoJob) {
			return job, err
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(q.cfg.PollInterval)
		select {
		case <-ctx.Done():
			return domain.Job{}, fmt.Errorf("waiting for job: %w", ctx.Err())
		case <-wake:
		case <-timer.C:
		}
	}
}

func leaseResult(res sql.Result, err error, id domain.JobID, op string) error {
	if err != nil {
		return fmt.Errorf("%s job %s: %w", op, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s job %s: %w", op, id, err)
	}
	if n == 0 {
		return fmt.Errorf("%s job %s: %w", op, id, domain.ErrLeaseLost)
	}
	return nil
}

// prefixed qualifies each column in a comma-separated list with table.
func prefixed(table, columns string) string {
	parts := strings.Split(columns, ",")
	for i, c := range parts {
		parts[i] = table + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

type rowScanner interface{ Scan(dest ...any) error }

// scanJob reads a row of jobColumns, followed by the values of extra if given.
func scanJob(r rowScanner, extra ...any) (domain.Job, error) {
	var (
		j                domain.Job
		id, exec, status string
		avail, created   time.Time
		owner, lastErr   sql.NullString
		until            sql.NullTime
	)
	dest := append([]any{&id, &exec, &status, &j.Attempts, &avail, &owner, &until, &lastErr, &created}, extra...)
	if err := r.Scan(dest...); err != nil {
		return domain.Job{}, err //nolint:wrapcheck // callers test for sql.ErrNoRows and wrap
	}
	j.ID, j.ExecutionID, j.Status = domain.JobID(id), domain.ExecutionID(exec), domain.JobStatus(status)
	j.LeaseOwner, j.LastError = owner.String, lastErr.String
	j.AvailableAt, j.CreatedAt = pgdb.FromDB(avail), pgdb.FromDB(created)
	if until.Valid {
		j.LeaseUntil = pgdb.FromDB(until.Time)
	}
	return j, nil
}
