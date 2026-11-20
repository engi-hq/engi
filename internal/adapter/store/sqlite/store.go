// Package sqlite implements the persistence ports (workflows, versions,
// executions, credentials) on SQLite through database/sql.
//
// Rows are mapped to domain types here; JSON columns use the jsoncodec adapter.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Store is the shared state of the SQLite persistence adapters. Workflows,
// Executions and Credentials return the port implementations (separate types,
// because the ports reuse method names such as Get and List). Store itself is
// the TxManager.
type Store struct {
	run *sqlitedb.Runner
	cfg config.StoreConfig
	clk port.Clock
}

// Workflows implements port.WorkflowStore and port.WorkflowVersionStore.
type Workflows struct{ *Store }

// Executions implements port.ExecutionStore and port.ExecutionStateStore.
type Executions struct{ *Store }

var (
	_ port.WorkflowStore        = (*Workflows)(nil)
	_ port.WorkflowVersionStore = (*Workflows)(nil)
	_ port.ExecutionStore       = (*Executions)(nil)
	_ port.ExecutionStateStore  = (*Executions)(nil)
	_ port.TxManager            = (*Store)(nil)
)

// Workflows returns the workflow and version stores.
func (s *Store) Workflows() *Workflows { return &Workflows{s} }

// Executions returns the execution and execution-state stores.
func (s *Store) Executions() *Executions { return &Executions{s} }

// New returns a Store over a migrated database opened with sqlitedb.Open.
func New(db *sql.DB, cfg config.StoreConfig, clk port.Clock) *Store {
	return &Store{run: sqlitedb.NewRunner(db), cfg: cfg, clk: clk}
}

// WithinTx implements port.TxManager.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.run.WithinTx(ctx, fn)
}

// limit resolves a requested page size against the configured default and cap.
func (s *Store) limit(p port.Page) int {
	switch {
	case p.Limit <= 0:
		return s.cfg.DefaultPageSize
	case p.Limit > s.cfg.MaxPageSize:
		return s.cfg.MaxPageSize
	}
	return p.Limit
}

func encodeCursor(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }

func decodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrInvalidCursor, err)
	}
	return string(b), nil
}

func sqliteCode(err error) int {
	var se *sqlitedrv.Error
	if errors.As(err, &se) {
		return se.Code()
	}
	return 0
}

func isUniqueViolation(err error) bool {
	c := sqliteCode(err)
	return c == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

func isPrimaryKeyViolation(err error) bool {
	return sqliteCode(err) == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func isForeignKeyViolation(err error) bool {
	return sqliteCode(err) == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

func notFound(what, id string) error {
	return fmt.Errorf("%s %q: %w", what, id, domain.ErrNotFound)
}

// timeArg renders an optional time as a SQL argument (NULL when nil).
func timeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return sqlitedb.FormatTime(*t)
}

// optTime parses a nullable time column.
func optTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid {
		return nil, nil
	}
	t, err := sqlitedb.ParseTime(v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
