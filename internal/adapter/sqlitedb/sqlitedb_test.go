package sqlitedb_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/migrations"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	cfg := config.Default().Store
	cfg.Path = filepath.Join(t.TempDir(), "a b.db")
	db, err := sqlitedb.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func tableCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenAppliesPragmas(t *testing.T) {
	db := openTemp(t)
	cfg := config.Default().Store
	checks := map[string]string{
		"journal_mode": cfg.JournalMode,
		"foreign_keys": "1",
		"busy_timeout": "5000",
	}
	for pragma, want := range checks {
		var got string
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestMigrateIsIdempotentAndReversible(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	for range 2 {
		if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
	}
	versions := embeddedVersions(t)
	want := len(versions)
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != want {
		t.Fatalf("schema_migrations rows = %d, %v; want %d", applied, err, want)
	}
	if n := tableCount(t, db); n < 7 {
		t.Fatalf("tables after up = %d", n)
	}
	// Each down reverts the latest migration; after all of them only
	// schema_migrations remains. Versions may have gaps (Postgres-only 0005).
	for i := len(versions) - 1; i >= 0; i-- {
		v, err := sqlitedb.MigrateDown(ctx, db, migrations.SQLite, migrations.SQLiteDir)
		if err != nil || v != versions[i] {
			t.Fatalf("MigrateDown = %d, %v; want %d", v, err, versions[i])
		}
	}
	if n := tableCount(t, db); n != 1 {
		t.Errorf("tables after down = %d, want 1", n)
	}
	if v, err := sqlitedb.MigrateDown(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil || v != 0 {
		t.Errorf("MigrateDown on empty = %d, %v; want 0", v, err)
	}
	if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
		t.Errorf("re-apply after down: %v", err)
	}
}

// embeddedVersions lists the versions of the up migrations compiled into the
// binary in ascending order, so the test does not change when one is added.
func embeddedVersions(t *testing.T) []int {
	t.Helper()
	entries, err := migrations.SQLite.ReadDir(migrations.SQLiteDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			t.Fatalf("migration %q: %v", e.Name(), err)
		}
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func TestMigrateConcurrentProcesses(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
				t.Errorf("Migrate: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestMigrateFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	fsys := fstest.MapFS{
		"m/0001_a.up.sql":   {Data: []byte(`CREATE TABLE a (id INTEGER);`)},
		"m/0002_bad.up.sql": {Data: []byte(`CREATE TABLE b (id INTEGER); THIS IS NOT SQL;`)},
	}
	err := sqlitedb.Migrate(ctx, db, fsys, "m")
	if err == nil || !strings.Contains(err.Error(), "0002_bad") {
		t.Fatalf("Migrate = %v, want failure naming 0002_bad", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('a', 'b')`).Scan(&n); err != nil || n != 1 {
		t.Errorf("tables a/b present = %d, %v; want only a (0001 kept, 0002 rolled back)", n, err)
	}
	var applied int
	_ = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied)
	if applied != 1 {
		t.Errorf("recorded migrations = %d, want 1", applied)
	}
}

func TestMigrateRejectsBadFileNames(t *testing.T) {
	db := openTemp(t)
	for name, fsys := range map[string]fstest.MapFS{
		"no number":    {"m/init.up.sql": {Data: []byte("SELECT 1;")}},
		"down only":    {"m/0001_x.down.sql": {Data: []byte("SELECT 1;")}},
		"zero version": {"m/0000_x.up.sql": {Data: []byte("SELECT 1;")}},
	} {
		if err := sqlitedb.Migrate(context.Background(), db, fsys, "m"); err == nil {
			t.Errorf("%s: Migrate succeeded", name)
		}
	}
}

func TestTimeRoundTripAndOrdering(t *testing.T) {
	a := time.Date(2026, 1, 1, 0, 0, 0, 500_000_000, time.UTC)
	b := a.Add(time.Microsecond)
	if sqlitedb.FormatTime(a) >= sqlitedb.FormatTime(b) {
		t.Error("formatted times do not sort like time")
	}
	if sqlitedb.FormatTime(a) >= sqlitedb.FormatTime(a.Add(time.Hour)) {
		t.Error("trailing zeros broke ordering")
	}
	got, err := sqlitedb.ParseTime(sqlitedb.FormatTime(b.In(time.FixedZone("x", 3600))))
	if err != nil || !got.Equal(b) {
		t.Errorf("round trip = %v, %v; want %v", got, err, b)
	}
}

// fixedClock is a port.Clock that always reads the same instant.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// applied_at must come from the injected clock, not the wall clock.
func TestMigrateRecordsAppliedAtFromClock(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	want := time.Date(2030, 2, 3, 4, 5, 6, 789_000, time.UTC)
	if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir, sqlitedb.WithClock(fixedClock{want})); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var version int
		var raw string
		if err := rows.Scan(&version, &raw); err != nil {
			t.Fatal(err)
		}
		got, err := sqlitedb.ParseTime(raw)
		if err != nil {
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
	if n == 0 {
		t.Fatal("no migrations recorded")
	}
}

func TestMigrateWithoutClockUsesSystemTime(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	before := time.Now().Add(-time.Second)
	if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT applied_at FROM schema_migrations ORDER BY version LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	got, err := sqlitedb.ParseTime(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Errorf("applied_at = %v, not near the system time", got)
	}
}
