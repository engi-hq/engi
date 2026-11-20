// Package sqlite implements the durable job queue on the SQLite jobs table.
//
// Claiming is one atomic UPDATE ... RETURNING on the oldest available job, so
// concurrent workers (in any process sharing the database file) never claim
// the same job. Enqueue wakes in-process waiters through a broadcast channel;
// jobs enqueued by other processes are found by the poll interval.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const jobColumns = `id, execution_id, status, attempts, available_at, lease_owner, lease_until, last_error, created_at`

// Queue implements port.JobQueue, port.JobReclaimer and port.JobDispatcher.
type Queue struct {
	run *sqlitedb.Runner
	cfg config.QueueConfig
	clk port.Clock

	mu   sync.Mutex
	wake chan struct{} // closed and replaced to wake every waiter
}

var (
	_ port.JobQueue      = (*Queue)(nil)
	_ port.JobReclaimer  = (*Queue)(nil)
	_ port.JobDispatcher = (*Queue)(nil)
	_ port.QueueStats    = (*Queue)(nil)
)

// New returns a Queue over a migrated database opened with sqlitedb.Open.
func New(db *sql.DB, cfg config.QueueConfig, clk port.Clock) *Queue {
	return &Queue{run: sqlitedb.NewRunner(db), cfg: cfg, clk: clk, wake: make(chan struct{})}
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
		sqlitedb.FormatTime(avail), sqlitedb.FormatTime(now), sqlitedb.FormatTime(now))
	var se *sqlitedrv.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return fmt.Errorf("enqueueing job %s: execution %s: %w", j.ID, j.ExecutionID, domain.ErrNotFound)
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("enqueueing job %s: %w", j.ID, domain.ErrAlreadyExists)
		}
	}
	if err != nil {
		return fmt.Errorf("enqueueing job %s: %w", j.ID, err)
	}
	q.notify()
	return nil
}

// Claim implements port.JobQueue.
func (q *Queue) Claim(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	now := q.clk.Now()
	var job domain.Job
	err := q.run.Do(ctx, func(db sqlitedb.DBTX) error {
		row := db.QueryRowContext(ctx,
			`UPDATE jobs SET status = ?, lease_owner = ?, lease_until = ?, attempts = attempts + 1, updated_at = ?
			 WHERE id = (SELECT id FROM jobs WHERE status = ? AND available_at <= ? ORDER BY available_at, id LIMIT 1)
			 RETURNING `+jobColumns,
			string(domain.JobLeased), workerID, sqlitedb.FormatTime(now.Add(lease)), sqlitedb.FormatTime(now),
			string(domain.JobQueued), sqlitedb.FormatTime(now))
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
		sqlitedb.FormatTime(now.Add(lease)), sqlitedb.FormatTime(now), string(id), workerID, string(domain.JobLeased))
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
		sqlitedb.FormatTime(retryAt), reason, sqlitedb.FormatTime(now), string(id), workerID, string(domain.JobLeased))
	if err != nil {
		return domain.Job{}, fmt.Errorf("failing job %s: %w", id, err)
	}
	if job.Status == domain.JobQueued {
		q.notify()
	}
	return job, nil
}

// Release implements port.JobQueue.
func (q *Queue) Release(ctx context.Context, id domain.JobID, workerID string) error {
	_, err := q.updateOwned(ctx, id, workerID,
		`UPDATE jobs SET status = ?, attempts = MAX(attempts - 1, 0), lease_owner = NULL, lease_until = NULL, updated_at = ?
		 WHERE id = ? AND lease_owner = ? AND status = ?
		 RETURNING `+jobColumns,
		string(domain.JobQueued), sqlitedb.FormatTime(q.clk.Now()), string(id), workerID, string(domain.JobLeased))
	if err != nil {
		return fmt.Errorf("releasing job %s: %w", id, err)
	}
	q.notify()
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
	now := sqlitedb.FormatTime(q.clk.Now())
	var out []domain.ReclaimedJob
	err := q.run.Do(ctx, func(db sqlitedb.DBTX) error {
		out = out[:0]
		// The UPDATE clears the owner, so read who held each expired lease first.
		owners, err := expiredOwners(ctx, db, now)
		if err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx,
			`UPDATE jobs SET status = CASE WHEN attempts >= ? THEN ? ELSE ? END,
				available_at = ?, lease_owner = NULL, lease_until = NULL, last_error = ?, updated_at = ?
			 WHERE status = ? AND lease_until < ?
			 RETURNING `+jobColumns,
			q.cfg.MaxAttempts, string(domain.JobFailed), string(domain.JobQueued),
			now, "lease expired", now, string(domain.JobLeased), now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			out = append(out, domain.ReclaimedJob{Job: j, PreviousOwner: owners[j.ID]})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("reclaiming expired jobs: %w", err)
	}
	for _, j := range out {
		if j.Status == domain.JobQueued {
			q.notify()
			break
		}
	}
	return out, nil
}

// expiredOwners maps each leased job whose lease ended before now to its owner.
func expiredOwners(ctx context.Context, db sqlitedb.DBTX, now string) (map[domain.JobID]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, lease_owner FROM jobs WHERE status = ? AND lease_until < ?`, string(domain.JobLeased), now)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the caller
	}
	defer rows.Close()
	owners := make(map[domain.JobID]string)
	for rows.Next() {
		var id string
		var owner sql.NullString
		if err := rows.Scan(&id, &owner); err != nil {
			return nil, err //nolint:wrapcheck // wrapped by the caller
		}
		owners[domain.JobID(id)] = owner.String
	}
	return owners, rows.Err() //nolint:wrapcheck // wrapped by the caller
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

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(r rowScanner) (domain.Job, error) {
	var (
		j                                domain.Job
		id, exec, status, avail, created string
		owner, until, lastErr            sql.NullString
	)
	if err := r.Scan(&id, &exec, &status, &j.Attempts, &avail, &owner, &until, &lastErr, &created); err != nil {
		return domain.Job{}, err //nolint:wrapcheck // callers test for sql.ErrNoRows and wrap
	}
	j.ID, j.ExecutionID, j.Status = domain.JobID(id), domain.ExecutionID(exec), domain.JobStatus(status)
	j.LeaseOwner, j.LastError = owner.String, lastErr.String
	var err error
	if j.AvailableAt, err = sqlitedb.ParseTime(avail); err != nil {
		return domain.Job{}, err
	}
	if j.CreatedAt, err = sqlitedb.ParseTime(created); err != nil {
		return domain.Job{}, err
	}
	if until.Valid {
		if j.LeaseUntil, err = sqlitedb.ParseTime(until.String); err != nil {
			return domain.Job{}, err
		}
	}
	return j, nil
}
