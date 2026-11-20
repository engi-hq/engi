package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/adapter/store/sqlite"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
	"github.com/engi-hq/engi/migrations"
)

func TestAuthStoreContract(t *testing.T) {
	porttest.RunAuthSuite(t, func(t *testing.T, clk port.Clock) porttest.AuthStores {
		t.Helper()
		cfg := config.Default().Store
		cfg.Path = filepath.Join(t.TempDir(), "auth.db")
		db, err := sqlitedb.Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitedb.Migrate(context.Background(), db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
		s := sqlite.New(db, cfg, clk)
		return porttest.AuthStores{Users: s.Users(), Sessions: s.Sessions(), APIKeys: s.APIKeys()}
	})
}
