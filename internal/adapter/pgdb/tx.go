package pgdb

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is what both *sql.DB and *sql.Tx offer.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// binder rebinds "?" placeholders before delegating.
type binder struct{ q DBTX }

func (b binder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return b.q.ExecContext(ctx, Rebind(query), args...) //nolint:wrapcheck // thin delegate
}

func (b binder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return b.q.QueryContext(ctx, Rebind(query), args...) //nolint:wrapcheck // thin delegate
}

func (b binder) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return b.q.QueryRowContext(ctx, Rebind(query), args...)
}

type txKey struct{ db *sql.DB }

// savepoint is the name used by Do for statement-level rollback inside a
// transaction. Postgres aborts a whole transaction on any error, so code that
// expects to recover from one (a unique violation, say) needs a savepoint.
const savepoint = "engi_do"

// Runner executes statements on db, or on the transaction carried by the
// context when there is one. Store and queue adapters sharing a database use
// the same Runner type, so one WithinTx covers both.
type Runner struct {
	db *sql.DB
}

// NewRunner returns a Runner for db.
func NewRunner(db *sql.DB) *Runner { return &Runner{db: db} }

// Q returns the transaction in ctx, or the database itself, with placeholders
// rebound.
func (r *Runner) Q(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{r.db}).(*sql.Tx); ok {
		return binder{tx}
	}
	return binder{r.db}
}

// Do runs fn in the ambient transaction, or in a new one that commits when fn
// returns nil. Inside an ambient transaction a failing fn is rolled back to a
// savepoint, so the caller can keep using the transaction.
func (r *Runner) Do(ctx context.Context, fn func(q DBTX) error) error {
	tx, ok := ctx.Value(txKey{r.db}).(*sql.Tx)
	if !ok {
		return r.WithinTx(ctx, func(ctx context.Context) error { return fn(r.Q(ctx)) })
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
		return fmt.Errorf("creating savepoint: %w", err)
	}
	if err := fn(binder{tx}); err != nil {
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rbErr != nil {
			return fmt.Errorf("%w (rollback to savepoint also failed: %v)", err, rbErr) //nolint:errorlint // err is the cause
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
		return fmt.Errorf("releasing savepoint: %w", err)
	}
	return nil
}

// WithinTx implements port.TxManager. Nested calls join the outer transaction.
func (r *Runner) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{r.db}).(*sql.Tx); ok {
		return fn(ctx)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	if err := fn(context.WithValue(ctx, txKey{r.db}, tx)); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w (rollback also failed: %v)", err, rbErr) //nolint:errorlint // err is the cause
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}
