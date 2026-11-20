// Package pgvectortest provides a database/sql driver that records statements
// and replays canned rows, so the vector store SQL and nodes are testable
// without PostgreSQL.
package pgvectortest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// Call is one recorded statement.
type Call struct {
	SQL  string
	Args []driver.Value
}

// DB is a fake database. Set the reply fields before running code under test.
type DB struct {
	mu sync.Mutex
	// Calls are the statements seen, in order (transaction control excluded).
	Calls []Call
	// Columns and Rows are returned by every query.
	Columns []string
	Rows    [][]driver.Value
	// Affected is the RowsAffected of every exec.
	Affected int64
	// FailOn makes a statement whose SQL contains the text fail with FailErr.
	FailOn  string
	FailErr error
	// Begins, Commits and Rollbacks count transaction events.
	Begins, Commits, Rollbacks int
}

var (
	registerOnce sync.Once
	registry     sync.Map // dsn -> *DB
	counter      int
	counterMu    sync.Mutex
)

const driverName = "pgvectortest"

// Open returns a *sql.DB backed by a fresh fake and the fake itself.
func Open(t testing.TB) (db *sql.DB, fake *DB) {
	t.Helper()
	registerOnce.Do(func() { sql.Register(driverName, fakeDriver{}) })
	counterMu.Lock()
	counter++
	dsn := fmt.Sprintf("fake-%d", counter)
	counterMu.Unlock()
	fake = &DB{}
	registry.Store(dsn, fake)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(); registry.Delete(dsn) })
	return db, fake
}

// SQLs returns the SQL text of every recorded call.
func (f *DB) SQLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Calls))
	for i, c := range f.Calls {
		out[i] = c.SQL
	}
	return out
}

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	v, ok := registry.Load(dsn)
	if !ok {
		return nil, errors.New("unknown fake database")
	}
	return &conn{db: v.(*DB)}, nil
}

type conn struct{ db *DB }

func (c *conn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (c *conn) Close() error                        { return nil }
func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.db.mu.Lock()
	c.db.Begins++
	c.db.mu.Unlock()
	return &tx{db: c.db}, nil
}

func (c *conn) record(query string, args []driver.NamedValue) error {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()
	vals := make([]driver.Value, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	c.db.Calls = append(c.db.Calls, Call{SQL: query, Args: vals})
	if c.db.FailOn != "" && strings.Contains(query, c.db.FailOn) {
		return c.db.FailErr
	}
	return nil
}

func (c *conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.record(query, args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(c.db.Affected), nil
}

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.record(query, args); err != nil {
		return nil, err
	}
	return &rows{cols: c.db.Columns, data: c.db.Rows}, nil
}

type tx struct{ db *DB }

func (t *tx) Commit() error {
	t.db.mu.Lock()
	t.db.Commits++
	t.db.mu.Unlock()
	return nil
}

func (t *tx) Rollback() error {
	t.db.mu.Lock()
	t.db.Rollbacks++
	t.db.mu.Unlock()
	return nil
}

type rows struct {
	cols []string
	data [][]driver.Value
	i    int
}

func (r *rows) Columns() []string { return r.cols }
func (r *rows) Close() error      { return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.i >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.i])
	r.i++
	return nil
}
