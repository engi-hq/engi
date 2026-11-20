//go:build integration

package postgres_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	queue "github.com/engi-hq/engi/internal/adapter/queue/postgres"
	store "github.com/engi-hq/engi/internal/adapter/store/postgres"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
)

func TestQueueContract(t *testing.T) {
	porttest.RunQueueSuite(t, func(t *testing.T, clk port.Clock, opts porttest.QueueOptions) porttest.QueueEnv {
		t.Helper()
		env := pgtest.New(t)
		qcfg := config.Default().Queue
		qcfg.MaxAttempts, qcfg.PollInterval = opts.MaxAttempts, opts.PollInterval
		q := queue.New(env.DB, qcfg, clk)
		if err := q.Listen(context.Background(), env.Target, time.Second, slog.Default()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(q.Close)
		s := store.New(env.DB, env.Store, clk)
		return porttest.QueueEnv{
			Queue: q, Reclaimer: q, Dispatcher: q, Stats: q,
			Workflows: s.Workflows(), Executions: s.Executions(), Tx: s,
		}
	})
}
