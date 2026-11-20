// Package postgres implements port.KeyLocker with transaction-scoped Postgres
// advisory locks. The lock lives as long as the transaction that fn runs in,
// so the store writes made by fn commit before any other replica can take the
// lock.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/port"
)

// lockTimeoutSetting is the parameter that bounds the wait for a lock.
const lockTimeoutSetting = "lock_timeout"

// Locker is a port.KeyLocker on Postgres advisory locks. Keys are hashed to
// the 64-bit lock space, so every replica that shares the database and the
// key names agrees on the lock.
type Locker struct {
	run         *pgdb.Runner
	waitTimeout time.Duration
}

var _ port.KeyLocker = (*Locker)(nil)

// New returns a Locker on db. A waiter gives up after waitTimeout, and ctx
// also ends the wait.
func New(db *sql.DB, waitTimeout time.Duration) *Locker {
	return &Locker{run: pgdb.NewRunner(db), waitTimeout: waitTimeout}
}

// WithLock implements port.KeyLocker. It opens a transaction (or joins the
// ambient one), takes the advisory lock for key, runs fn and commits when fn
// returns nil. A failing fn rolls its writes back.
func (l *Locker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	return l.run.WithinTx(ctx, func(ctx context.Context) error {
		q := l.run.Q(ctx)
		if _, err := q.ExecContext(ctx, `SELECT set_config(?, ?, true)`,
			lockTimeoutSetting, fmt.Sprintf("%dms", l.waitTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("setting lock timeout for %q: %w", key, err)
		}
		if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, Hash(key)); err != nil {
			return fmt.Errorf("taking advisory lock for %q: %w", key, err)
		}
		return fn(ctx)
	})
}

// Hash maps a lock key to the bigint advisory lock space. The FNV-1a 64-bit
// hash is stable across processes and releases, which a lock needs.
func Hash(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key)) // hash.Hash.Write never fails
	return int64(h.Sum64())     //nolint:gosec // the wrap to a signed bigint is the point: any 64 bits are a valid lock key
}
