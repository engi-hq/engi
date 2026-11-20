package pgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/port"
)

const (
	upSuffix   = ".up.sql"
	downSuffix = ".down.sql"

	// migrationLockKey is the transaction-level advisory lock serializing
	// migrations of replicas that start at once.
	migrationLockKey = 7313400
)

type migration struct {
	version int
	name    string
	up      string
	down    string
}

// Migrate applies every pending "NNNN_name.up.sql" in dir of fsys, in order,
// each in its own transaction, and records it in schema_migrations. It is
// idempotent and safe when several replicas start at once: an advisory lock
// serializes them and each re-checks what is applied inside its transaction.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, dir string, opts ...Option) error {
	o := newOptions(opts)
	migs, err := load(fsys, dir)
	if err != nil {
		return err
	}
	if err := ensureTable(ctx, db); err != nil {
		return err
	}
	for _, m := range migs {
		if err := apply(ctx, db, m, o); err != nil {
			return err
		}
	}
	return nil
}

// Option configures Migrate.
type Option func(*options)

// options is the resolved configuration of one Migrate call.
type options struct {
	clock port.Clock
}

// WithClock sets the time source for applied_at. Without it the system clock
// is used; the application passes its own clock so tests can fix the time.
func WithClock(clk port.Clock) Option {
	return func(o *options) {
		if clk != nil {
			o.clock = clk
		}
	}
}

// systemClock is the default time source of Migrate.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func newOptions(opts []Option) options {
	o := options{clock: systemClock{}}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// MigrateDown reverts the most recently applied migration, if any, and returns
// its version (zero when nothing was applied).
func MigrateDown(ctx context.Context, db *sql.DB, fsys fs.FS, dir string) (int, error) {
	migs, err := load(fsys, dir)
	if err != nil {
		return 0, err
	}
	if err := ensureTable(ctx, db); err != nil {
		return 0, err
	}
	var latest sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&latest); err != nil {
		return 0, fmt.Errorf("reading latest migration: %w", err)
	}
	if !latest.Valid {
		return 0, nil
	}
	for _, m := range migs {
		if m.version != int(latest.Int64) {
			continue
		}
		if m.down == "" {
			return 0, fmt.Errorf("migration %04d_%s has no down script", m.version, m.name)
		}
		err := NewRunner(db).WithinTx(ctx, func(ctx context.Context) error {
			q := NewRunner(db).Q(ctx)
			if err := lock(ctx, q); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, m.down); err != nil {
				return fmt.Errorf("running down %04d_%s: %w", m.version, m.name, err)
			}
			if _, err := q.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, m.version); err != nil {
				return fmt.Errorf("unrecording migration %d: %w", m.version, err)
			}
			return nil
		})
		return m.version, err
	}
	return 0, fmt.Errorf("applied migration %d has no file", latest.Int64)
}

func lock(ctx context.Context, q DBTX) error {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, migrationLockKey); err != nil {
		return fmt.Errorf("taking migration lock: %w", err)
	}
	return nil
}

func ensureTable(ctx context.Context, db *sql.DB) error {
	r := NewRunner(db)
	err := r.WithinTx(ctx, func(ctx context.Context) error {
		q := r.Q(ctx)
		if err := lock(ctx, q); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)`)
		return err
	})
	if err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	return nil
}

func apply(ctx context.Context, db *sql.DB, m migration, o options) error {
	r := NewRunner(db)
	return r.WithinTx(ctx, func(ctx context.Context) error {
		q := r.Q(ctx)
		if err := lock(ctx, q); err != nil {
			return err
		}
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&n); err != nil {
			return fmt.Errorf("checking migration %d: %w", m.version, err)
		}
		if n > 0 {
			return nil
		}
		if _, err := q.ExecContext(ctx, m.up); err != nil {
			return fmt.Errorf("running %04d_%s: %w", m.version, m.name, err)
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, Stamp(o.clock.Now())); err != nil {
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}
		return nil
	})
}

func load(fsys fs.FS, dir string) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("reading migrations dir %q: %w", dir, err)
	}
	byVersion := map[int]*migration{}
	for _, e := range entries {
		file := e.Name()
		var stem string
		var isUp bool
		switch {
		case strings.HasSuffix(file, upSuffix):
			stem, isUp = strings.TrimSuffix(file, upSuffix), true
		case strings.HasSuffix(file, downSuffix):
			stem = strings.TrimSuffix(file, downSuffix)
		default:
			continue
		}
		verText, name, ok := strings.Cut(stem, "_")
		version, convErr := strconv.Atoi(verText)
		if !ok || convErr != nil || version <= 0 {
			return nil, fmt.Errorf("migration file %q must be named NNNN_name.up.sql or .down.sql", file)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, file))
		if err != nil {
			return nil, fmt.Errorf("reading migration %q: %w", file, err)
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{version: version, name: name}
			byVersion[version] = m
		}
		if isUp {
			m.up = string(body)
		} else {
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.up == "" {
			return nil, errors.New("migration " + strconv.Itoa(m.version) + " has a down script but no up script")
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Pending returns how many migrations in dir have not been applied yet, without
// applying any. A database that was never migrated has all of them pending.
func Pending(ctx context.Context, db *sql.DB, fsys fs.FS, dir string) (int, error) {
	migs, err := load(fsys, dir)
	if err != nil {
		return 0, err
	}
	if err := ensureTable(ctx, db); err != nil {
		return 0, err
	}
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return 0, fmt.Errorf("counting applied migrations: %w", err)
	}
	return max(len(migs)-applied, 0), nil
}
