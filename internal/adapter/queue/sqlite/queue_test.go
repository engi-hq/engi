package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	queue "github.com/engi-hq/engi/internal/adapter/queue/sqlite"
	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	store "github.com/engi-hq/engi/internal/adapter/store/sqlite"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
	"github.com/engi-hq/engi/migrations"
)

func TestQueueContract(t *testing.T) {
	porttest.RunQueueSuite(t, func(t *testing.T, clk port.Clock, opts porttest.QueueOptions) porttest.QueueEnv {
		t.Helper()
		scfg := config.Default().Store
		scfg.Path = filepath.Join(t.TempDir(), "engi.db")
		db, err := sqlitedb.Open(context.Background(), scfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitedb.Migrate(context.Background(), db, migrations.SQLite, migrations.SQLiteDir); err != nil {
			t.Fatal(err)
		}
		qcfg := config.Default().Queue
		qcfg.MaxAttempts, qcfg.PollInterval = opts.MaxAttempts, opts.PollInterval
		q := queue.New(db, qcfg, clk)
		s := store.New(db, scfg, clk)
		return porttest.QueueEnv{
			Queue: q, Reclaimer: q, Dispatcher: q, Stats: q,
			Workflows: s.Workflows(), Executions: s.Executions(), Tx: s,
		}
	})
}
