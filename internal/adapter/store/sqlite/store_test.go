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

func TestStoreContract(t *testing.T) {
	porttest.RunStoreSuite(t, func(t *testing.T, clk port.Clock) porttest.Stores {
		t.Helper()
		cfg := config.Default().Store
		cfg.Path = filepath.Join(t.TempDir(), "engi test.db") // a space in the path is deliberate
		db, err := sqlitedb.Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitedb.Migrate(context.Background(), db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
		s := sqlite.New(db, cfg, clk)
		return porttest.Stores{
			Workflows: s.Workflows(), Versions: s.Workflows(),
			Executions: s.Executions(), States: s.Executions(),
			Credentials: s.Credentials(), Tags: s.Tags(), Variables: s.Variables(), Tx: s,
		}
	})
}

func TestChatMemoryContract(t *testing.T) {
	porttest.RunChatMemorySuite(t, func(t *testing.T, clk port.Clock, mem config.AIMemoryConfig) port.ChatMemoryStore {
		t.Helper()
		cfg := config.Default().Store
		cfg.Path = filepath.Join(t.TempDir(), "engi.db")
		db, err := sqlitedb.Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitedb.Migrate(context.Background(), db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
		return sqlite.New(db, cfg, clk).ChatMemory(mem)
	})
}

func TestTestWebhookContract(t *testing.T) {
	porttest.RunTestWebhookSuite(t, func(t *testing.T, clk port.Clock) porttest.TestWebhookEnv {
		t.Helper()
		cfg := config.Default().Store
		cfg.Path = filepath.Join(t.TempDir(), "engi.db")
		db, err := sqlitedb.Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitedb.Migrate(context.Background(), db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
		s := sqlite.New(db, cfg, clk)
		return porttest.TestWebhookEnv{Tests: s.TestWebhooks(), Workflows: s.Workflows()}
	})
}
