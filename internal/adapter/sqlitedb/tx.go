package sqlitedb

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

type txKey struct{ db *sql.DB }

// Runner executes statements on db, or on the transaction carried by the
// context when there is one. Store and queue adapters sharing a database use
// the same Runner type, so one WithinTx covers both.
type Runner struct {
	db *sql.DB
}

// NewRunner returns a Runner for db.
func NewRunner(db *sql.DB) *Runner { return &Runner{db: db} }

// Q returns the transaction in ctx, or the database itself.
func (r *Runner) Q(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{r.db}).(*sql.Tx); ok {
		return tx
	}
	return r.db
}

// Do runs fn in the ambient transaction, or in a new one that commits when fn
// returns nil. Use it for multi-statement operations that must be atomic.
func (r *Runner) Do(ctx context.Context, fn func(q DBTX) error) error {
	if _, ok := ctx.Value(txKey{r.db}).(*sql.Tx); ok {
		return fn(r.Q(ctx))
	}
	return r.WithinTx(ctx, func(ctx context.Context) error { return fn(r.Q(ctx)) })
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
