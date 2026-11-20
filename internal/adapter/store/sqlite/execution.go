package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// executionInsertColumns are the columns Create writes; the recovery columns
// keep their defaults until ClaimRun and RequeueRun set them.
const executionInsertColumns = `id, workflow_id, workflow_version, mode, status, started_at, finished_at,
	wait_until, dedup_key, parent_execution_id, error`

// executionColumns are the columns every read returns, in scan order.
const executionColumns = executionInsertColumns + `, attempts, last_recovered_at`

// Create implements port.ExecutionStore.
func (s *Executions) Create(ctx context.Context, e domain.Execution) error {
	errJSON, err := jsoncodec.EncodeErrorInfo(e.Error)
	if err != nil {
		return fmt.Errorf("creating execution %s: %w", e.ID, err)
	}
	runJSON, err := jsoncodec.EncodeRunData(e.RunData)
	if err != nil {
		return fmt.Errorf("creating execution %s: %w", e.ID, err)
	}
	var dedup, parent any
	if e.DedupKey != "" {
		dedup = e.DedupKey
	}
	if e.ParentExecutionID != nil {
		parent = string(*e.ParentExecutionID)
	}
	err = s.run.Do(ctx, func(q sqlitedb.DBTX) error {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO executions (`+executionInsertColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(e.ID), string(e.WorkflowID), e.WorkflowVersion, string(e.Mode), string(e.Status),
			sqlitedb.FormatTime(e.StartedAt), timeArg(e.FinishedAt), timeArg(e.WaitUntil), dedup, parent,
			string(errJSON)); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `INSERT INTO execution_data (execution_id, run_data) VALUES (?, ?)`,
			string(e.ID), string(runJSON))
		return err
	})
	switch {
	case err == nil:
		return nil
	case isPrimaryKeyViolation(err):
		return fmt.Errorf("execution %s: %w", e.ID, domain.ErrAlreadyExists)
	case isUniqueViolation(err) && e.DedupKey != "":
		return s.duplicate(ctx, e)
	case isForeignKeyViolation(err):
		return fmt.Errorf("execution %s references an unknown workflow version: %w", e.ID, domain.ErrNotFound)
	}
	return fmt.Errorf("creating execution %s: %w", e.ID, err)
}

// duplicate builds the typed duplicate error, naming the holder of the key.
func (s *Executions) duplicate(ctx context.Context, e domain.Execution) error {
	derr := &domain.DuplicateError{WorkflowID: e.WorkflowID, DedupKey: e.DedupKey}
	var existing string
	err := s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT id FROM executions WHERE workflow_id = ? AND dedup_key = ?`,
		string(e.WorkflowID), e.DedupKey).Scan(&existing)
	if err == nil {
		derr.ExistingID = domain.ExecutionID(existing)
	}
	return derr
}

// Get implements port.ExecutionStore.
func (s *Executions) Get(ctx context.Context, id domain.ExecutionID) (domain.Execution, error) {
	row := s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT e.id, e.workflow_id, e.workflow_version, e.mode, e.status, e.started_at, e.finished_at,
			e.wait_until, e.dedup_key, e.parent_execution_id, e.error, e.attempts, e.last_recovered_at, d.run_data
		 FROM executions e JOIN execution_data d ON d.execution_id = e.id WHERE e.id = ?`, string(id))
	e, err := scanExecution(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Execution{}, notFound("execution", string(id))
	}
	if err != nil {
		return domain.Execution{}, fmt.Errorf("reading execution %s: %w", id, err)
	}
	return e, nil
}

// Delete implements port.ExecutionStore. execution_data and jobs follow by cascade.
func (s *Executions) Delete(ctx context.Context, id domain.ExecutionID) error {
	res, err := s.run.Q(ctx).ExecContext(ctx, `DELETE FROM executions WHERE id = ?`, string(id))
	return affectedOrNotFound(res, err, "execution", string(id))
}

type rowScanner interface{ Scan(dest ...any) error }

func scanExecution(r rowScanner, withData bool) (domain.Execution, error) {
	var (
		e                                      domain.Execution
		wfID, mode, status, started            string
		finished, wait, dedup, parent, errJSON sql.NullString
		recovered, runJSON                     sql.NullString
	)
	dest := []any{&e.ID, &wfID, &e.WorkflowVersion, &mode, &status, &started, &finished, &wait, &dedup, &parent, &errJSON,
		&e.Attempts, &recovered}
	if withData {
		dest = append(dest, &runJSON)
	}
	if err := r.Scan(dest...); err != nil {
		return domain.Execution{}, err //nolint:wrapcheck // callers wrap and test for sql.ErrNoRows
	}
	e.WorkflowID, e.Mode, e.Status = domain.WorkflowID(wfID), domain.ExecutionMode(mode), domain.ExecutionStatus(status)
	var err error
	if e.StartedAt, err = sqlitedb.ParseTime(started); err != nil {
		return domain.Execution{}, err
	}
	if e.FinishedAt, err = optTime(finished); err != nil {
		return domain.Execution{}, err
	}
	if e.WaitUntil, err = optTime(wait); err != nil {
		return domain.Execution{}, err
	}
	e.DedupKey = dedup.String
	if parent.Valid {
		p := domain.ExecutionID(parent.String)
		e.ParentExecutionID = &p
	}
	if errJSON.Valid {
		if e.Error, err = jsoncodec.DecodeErrorInfo([]byte(errJSON.String)); err != nil {
			return domain.Execution{}, fmt.Errorf("decoding execution error: %w", err)
		}
	}
	if e.LastRecoveredAt, err = optTime(recovered); err != nil {
		return domain.Execution{}, err
	}
	if withData {
		if e.RunData, err = jsoncodec.DecodeRunData([]byte(runJSON.String)); err != nil {
			return domain.Execution{}, fmt.Errorf("decoding execution run data: %w", err)
		}
	}
	return e, nil
}

// List implements port.ExecutionStore.
func (s *Executions) List(ctx context.Context, f port.ExecutionFilter, p port.Page) (port.PageResult[domain.Execution], error) {
	after, err := decodeCursor(p.Cursor)
	if err != nil {
		return port.PageResult[domain.Execution]{}, err
	}
	var where []string
	var args []any
	if after != "" {
		where, args = append(where, "id < ?"), append(args, after)
	}
	if f.WorkflowID != "" {
		where, args = append(where, "workflow_id = ?"), append(args, string(f.WorkflowID))
	}
	if len(f.Statuses) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",")
		where = append(where, "status IN ("+marks+")")
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	if !f.StartedAfter.IsZero() {
		where, args = append(where, "started_at >= ?"), append(args, sqlitedb.FormatTime(f.StartedAfter))
	}
	if !f.StartedBefore.IsZero() {
		where, args = append(where, "started_at < ?"), append(args, sqlitedb.FormatTime(f.StartedBefore))
	}
	n := s.limit(p)
	rows, err := s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+executionColumns+` FROM executions`+whereClause(where)+
			` ORDER BY id DESC LIMIT `+strconv.Itoa(n+1), args...)
	if err != nil {
		return port.PageResult[domain.Execution]{}, fmt.Errorf("listing executions: %w", err)
	}
	defer rows.Close()
	var items []domain.Execution
	var keys []string
	for rows.Next() {
		e, err := scanExecution(rows, false)
		if err != nil {
			return port.PageResult[domain.Execution]{}, fmt.Errorf("scanning execution: %w", err)
		}
		items, keys = append(items, e), append(keys, string(e.ID))
	}
	if err := rows.Err(); err != nil {
		return port.PageResult[domain.Execution]{}, fmt.Errorf("iterating executions: %w", err)
	}
	return page(items, keys, n), nil
}

// ClaimRun implements port.ExecutionStore. The conditional UPDATE is the whole
// claim: two jobs racing for one execution cannot both match status queued.
func (s *Executions) ClaimRun(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder, claimedAt time.Time) (domain.Execution, bool, error) {
	var affected int64
	err := s.run.Do(ctx, func(q sqlitedb.DBTX) error {
		res, err := q.ExecContext(ctx,
			`UPDATE executions SET status = ?, wait_until = NULL, attempts = attempts + 1,
			   run_job_id = ?, run_job_attempt = ?,
			   last_recovered_at = CASE WHEN status = ? THEN ? ELSE last_recovered_at END
			 WHERE id = ? AND (status = ? OR (status = ? AND run_job_id = ? AND run_job_attempt < ?))`,
			string(domain.StatusRunning), string(holder.JobID), holder.Attempt,
			string(domain.StatusRunning), timeArg(&claimedAt),
			string(id), string(domain.StatusQueued), string(domain.StatusRunning), string(holder.JobID), holder.Attempt)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("counting claimed rows: %w", err)
		}
		affected = n
		return nil
	})
	if err != nil {
		return domain.Execution{}, false, fmt.Errorf("claiming execution %s: %w", id, err)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return domain.Execution{}, false, fmt.Errorf("claiming execution %s: %w", id, err)
	}
	return current, affected == 1, nil
}

// fencedWrite writes the result columns of e and its run data when the fence
// matches the row. fence is a WHERE clause over the status and holder columns;
// fenceArgs fill its placeholders. It reports whether the row matched.
func (s *Executions) fencedWrite(ctx context.Context, e domain.Execution, fence string, fenceArgs ...any) (bool, error) {
	errJSON, err := jsoncodec.EncodeErrorInfo(e.Error)
	if err != nil {
		return false, fmt.Errorf("writing execution %s: %w", e.ID, err)
	}
	runJSON, err := jsoncodec.EncodeRunData(e.RunData)
	if err != nil {
		return false, fmt.Errorf("writing execution %s: %w", e.ID, err)
	}
	written := false
	err = s.run.Do(ctx, func(q sqlitedb.DBTX) error {
		args := append([]any{string(e.Status), timeArg(e.FinishedAt), timeArg(e.WaitUntil), string(errJSON), string(e.ID)}, fenceArgs...)
		res, err := q.ExecContext(ctx,
			`UPDATE executions SET status = ?, finished_at = ?, wait_until = ?, error = ? WHERE id = ? AND `+fence, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("counting written rows: %w", err)
		}
		if n == 0 {
			return nil
		}
		written = true
		_, err = q.ExecContext(ctx, `UPDATE execution_data SET run_data = ? WHERE execution_id = ?`,
			string(runJSON), string(e.ID))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("writing execution %s: %w", e.ID, err)
	}
	return written, nil
}

// FinishRun implements port.ExecutionStore.
func (s *Executions) FinishRun(ctx context.Context, e domain.Execution, holder domain.RunHolder) (bool, error) {
	return s.fencedWrite(ctx, e, `status = ? AND run_job_id = ? AND run_job_attempt = ?`,
		string(domain.StatusRunning), string(holder.JobID), holder.Attempt)
}

// CrashRun implements port.ExecutionStore.
func (s *Executions) CrashRun(ctx context.Context, e domain.Execution, holder domain.RunHolder) (bool, error) {
	return s.fencedWrite(ctx, e,
		`(status = ? OR (status = ? AND run_job_id = ? AND run_job_attempt = ?))`,
		string(domain.StatusQueued), string(domain.StatusRunning), string(holder.JobID), holder.Attempt)
}

// RequeueRun implements port.ExecutionStore.
func (s *Executions) RequeueRun(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder, recoveredAt *time.Time) (bool, error) {
	res, err := s.run.Q(ctx).ExecContext(ctx,
		`UPDATE executions SET status = ?, last_recovered_at = COALESCE(?, last_recovered_at)
		 WHERE id = ? AND status = ? AND run_job_id = ? AND run_job_attempt = ?`,
		string(domain.StatusQueued), timeArg(recoveredAt), string(id), string(domain.StatusRunning),
		string(holder.JobID), holder.Attempt)
	if err != nil {
		return false, fmt.Errorf("requeueing execution %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("requeueing execution %s: %w", id, err)
	}
	return n == 1, nil
}

// Update implements port.ExecutionStore.
func (s *Executions) Update(ctx context.Context, e domain.Execution) error {
	errJSON, err := jsoncodec.EncodeErrorInfo(e.Error)
	if err != nil {
		return fmt.Errorf("updating execution %s: %w", e.ID, err)
	}
	runJSON, err := jsoncodec.EncodeRunData(e.RunData)
	if err != nil {
		return fmt.Errorf("updating execution %s: %w", e.ID, err)
	}
	err = s.run.Do(ctx, func(q sqlitedb.DBTX) error {
		res, err := q.ExecContext(ctx,
			`UPDATE executions SET status = ?, finished_at = ?, wait_until = ?, error = ? WHERE id = ?`,
			string(e.Status), timeArg(e.FinishedAt), timeArg(e.WaitUntil), string(errJSON), string(e.ID))
		if err := affectedOrNotFound(res, err, "execution", string(e.ID)); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE execution_data SET run_data = ? WHERE execution_id = ?`,
			string(runJSON), string(e.ID))
		return err
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return fmt.Errorf("updating execution %s: %w", e.ID, err)
	}
	return nil
}

// FindDueWaiting implements port.ExecutionStore.
func (s *Executions) FindDueWaiting(ctx context.Context, now time.Time, limit int) ([]domain.Execution, error) {
	rows, err := s.run.Q(ctx).QueryContext(ctx,
		`SELECT `+executionColumns+` FROM executions
		 WHERE status = ? AND wait_until IS NOT NULL AND wait_until <= ?
		 ORDER BY wait_until, id LIMIT ?`,
		string(domain.StatusWaiting), sqlitedb.FormatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("finding due executions: %w", err)
	}
	defer rows.Close()
	var out []domain.Execution
	for rows.Next() {
		e, err := scanExecution(rows, false)
		if err != nil {
			return nil, fmt.Errorf("scanning due execution: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating due executions: %w", err)
	}
	return out, nil
}

// Prune implements port.ExecutionStore.
func (s *Executions) Prune(ctx context.Context, cutoffs map[domain.ExecutionStatus]time.Time) (int, error) {
	total := 0
	err := s.run.Do(ctx, func(q sqlitedb.DBTX) error {
		for status, cutoff := range cutoffs {
			if !status.Terminal() {
				continue
			}
			res, err := q.ExecContext(ctx,
				`DELETE FROM executions WHERE status = ? AND COALESCE(finished_at, started_at) < ?`,
				string(status), sqlitedb.FormatTime(cutoff))
			if err != nil {
				return fmt.Errorf("pruning %s executions: %w", status, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("counting pruned executions: %w", err)
			}
			total += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// SaveState implements port.ExecutionStateStore.
func (s *Executions) SaveState(ctx context.Context, id domain.ExecutionID, state []byte) error {
	res, err := s.run.Q(ctx).ExecContext(ctx,
		`UPDATE execution_data SET state = ? WHERE execution_id = ?`, string(state), string(id))
	return affectedOrNotFound(res, err, "execution", string(id))
}

// LoadState implements port.ExecutionStateStore.
func (s *Executions) LoadState(ctx context.Context, id domain.ExecutionID) ([]byte, error) {
	var state sql.NullString
	err := s.run.Q(ctx).QueryRowContext(ctx,
		`SELECT state FROM execution_data WHERE execution_id = ?`, string(id)).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !state.Valid) {
		return nil, notFound("execution state", string(id))
	}
	if err != nil {
		return nil, fmt.Errorf("loading state of execution %s: %w", id, err)
	}
	return []byte(state.String), nil
}

// DeleteState implements port.ExecutionStateStore. Deleting a missing state is not an error.
func (s *Executions) DeleteState(ctx context.Context, id domain.ExecutionID) error {
	if _, err := s.run.Q(ctx).ExecContext(ctx,
		`UPDATE execution_data SET state = NULL WHERE execution_id = ?`, string(id)); err != nil {
		return fmt.Errorf("deleting state of execution %s: %w", id, err)
	}
	return nil
}
