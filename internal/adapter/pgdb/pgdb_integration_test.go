//go:build integration

package pgdb_test

import (
	"context"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	"github.com/engi-hq/engi/migrations"
)

func countTables(t *testing.T, env *pgtest.Env) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateIsIdempotentConcurrentAndReversible(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t) // already migrated once
	var wg sync.WaitGroup
	for range 4 { // replicas starting at once
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pgdb.Migrate(ctx, env.DB, migrations.Postgres, migrations.PostgresDir); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	files, err := fs.Glob(migrations.Postgres, "postgres/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqliteFiles, _ := fs.Glob(migrations.SQLite, "sqlite/*.up.sql")
	if len(files) < len(sqliteFiles) {
		t.Fatalf("postgres has %d migrations, sqlite has %d", len(files), len(sqliteFiles))
	}
	if n, err := pgdb.Pending(ctx, env.DB, migrations.Postgres, migrations.PostgresDir); err != nil || n != 0 {
		t.Fatalf("pending = %d, %v", n, err)
	}
	for i := len(files); i >= 1; i-- {
		if v, err := pgdb.MigrateDown(ctx, env.DB, migrations.Postgres, migrations.PostgresDir); err != nil || v != i {
			t.Fatalf("MigrateDown = %d, %v; want %d", v, err, i)
		}
	}
	if n := countTables(t, env); n != 1 {
		t.Errorf("tables after down = %d, want only schema_migrations", n)
	}
	if err := pgdb.Migrate(ctx, env.DB, migrations.Postgres, migrations.PostgresDir); err != nil {
		t.Fatal(err)
	}
}

// fixedClock is a port.Clock that always reads the same instant.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// applied_at must come from the injected clock, not the wall clock.
func TestMigrateRecordsAppliedAtFromClock(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	files, err := fs.Glob(migrations.Postgres, "postgres/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range files { // roll back everything so Migrate re-applies it
		if _, err := pgdb.MigrateDown(ctx, env.DB, migrations.Postgres, migrations.PostgresDir); err != nil {
			t.Fatal(err)
		}
	}
	want := time.Date(2030, 2, 3, 4, 5, 6, 789_000, time.UTC)
	if err := pgdb.Migrate(ctx, env.DB, migrations.Postgres, migrations.PostgresDir, pgdb.WithClock(fixedClock{want})); err != nil {
		t.Fatal(err)
	}
	rows, err := env.DB.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var version int
		var got time.Time
		if err := rows.Scan(&version, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(want) {
			t.Errorf("migration %d applied_at = %v, want the clock's %v", version, got, want)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(files) {
		t.Fatalf("recorded %d migrations, want %d", n, len(files))
	}
}

func TestRebindSkipsLiterals(t *testing.T) {
	got := pgdb.Rebind(`SELECT '?' WHERE a = ? AND b = ?`)
	if want := `SELECT '?' WHERE a = $1 AND b = $2`; got != want {
		t.Errorf("Rebind = %q, want %q", got, want)
	}
}

func TestTargetKeepsDSNSecretOutOfErrors(t *testing.T) {
	cfg := pgtest.New(t).Store.Postgres
	cfg.DSN = "postgres://u:hunter2@host:notaport/db"
	_, err := pgdb.NewTarget(cfg, nil)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error must exist and not echo the password: %v", err)
	}
}
