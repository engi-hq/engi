package sqlite_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/adapter/store/sqlite"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/retention"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/migrations"
)

type noSleep struct{}

func (noSleep) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

type alwaysLeader struct{}

func (alwaysLeader) Leading() bool { return true }

func TestRetentionPrunesSQLite(t *testing.T) {
	ctx := context.Background()
	clk := servicetest.NewClock()
	cfg := config.Default().Store
	cfg.Path = filepath.Join(t.TempDir(), "retention.db")
	db, err := sqlitedb.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
		t.Fatal(err)
	}
	s := sqlite.New(db, cfg, clk)
	ids := servicetest.NewIDs(clk)
	wid, err := ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	wf := domain.Workflow{ID: wid, Name: "w", Nodes: []domain.Node{{ID: "nd_a", Name: "Start", Type: "engi.core.manual", TypeVersion: 1}}}
	if _, err := s.Workflows().Save(ctx, wf); err != nil {
		t.Fatal(err)
	}
	mk := func(st domain.ExecutionStatus, age time.Duration) domain.ExecutionID {
		id, err := ids.ExecutionID()
		if err != nil {
			t.Fatal(err)
		}
		fin := clk.Now().Add(-age)
		x := domain.Execution{ID: id, WorkflowID: wid, WorkflowVersion: 1, Mode: domain.ModeManual, Status: st, StartedAt: fin, FinishedAt: &fin}
		if err := s.Executions().Create(ctx, x); err != nil {
			t.Fatal(err)
		}
		return id
	}
	day := 24 * time.Hour
	oldOK, oldErr, newOK := mk(domain.StatusSuccess, 30*day), mk(domain.StatusError, 40*day), mk(domain.StatusSuccess, day)

	rc := config.Default().Retention
	rc.Enabled, rc.BatchSize = true, 1
	svc := retention.New(rc, retention.Deps{Executions: s.Executions(), Leader: alwaysLeader{}, Clock: clk, Sleeper: noSleep{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if n, err := svc.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("RunOnce = %d, %v; want 2", n, err)
	}
	for _, id := range []domain.ExecutionID{oldOK, oldErr} {
		if _, err := s.Executions().Get(ctx, id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("execution %s kept: %v", id, err)
		}
	}
	if _, err := s.Executions().Get(ctx, newOK); err != nil {
		t.Fatalf("recent execution pruned: %v", err)
	}
}
