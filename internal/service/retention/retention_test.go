package retention_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/retention"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

type execs struct {
	mu   sync.Mutex
	rows map[domain.ExecutionID]domain.Execution
}

func (e *execs) Get(_ context.Context, id domain.ExecutionID) (domain.Execution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x, ok := e.rows[id]
	if !ok {
		return x, domain.ErrNotFound
	}
	return x, nil
}

func (e *execs) List(_ context.Context, f port.ExecutionFilter, p port.Page) (port.PageResult[domain.Execution], error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.Execution
	for _, x := range e.rows {
		if len(out) < p.Limit && x.Status == f.Statuses[0] && x.StartedAt.Before(f.StartedBefore) {
			out = append(out, x)
		}
	}
	return port.PageResult[domain.Execution]{Items: out}, nil
}

func (e *execs) Delete(_ context.Context, id domain.ExecutionID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.rows, id)
	return nil
}

type blobs struct {
	deleted []domain.ExecutionID
	swept   int
}

func (b *blobs) DeleteExecution(_ context.Context, id domain.ExecutionID) error {
	b.deleted = append(b.deleted, id)
	return nil
}

func (b *blobs) Sweep(context.Context, time.Time, func(context.Context, domain.ExecutionID) (bool, error)) (int, error) {
	b.swept++
	return 0, nil
}

type leader bool

func (l leader) Leading() bool { return bool(l) }

// sleeper ticks once per tick receive, then blocks until ctx ends.
type sleeper struct{ ticks chan struct{} }

func (s sleeper) Sleep(ctx context.Context, _ time.Duration) error {
	select {
	case <-s.ticks:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cfg() config.RetentionConfig {
	c := config.Default().Retention
	c.Enabled, c.BatchSize = true, 2
	return c
}

func fixture(t *testing.T, clk *servicetest.Clock, specs map[domain.ExecutionStatus][]time.Duration) *execs {
	t.Helper()
	ids := servicetest.NewIDs(clk)
	e := &execs{rows: map[domain.ExecutionID]domain.Execution{}}
	for st, ages := range specs {
		for _, age := range ages {
			id, err := ids.ExecutionID()
			if err != nil {
				t.Fatal(err)
			}
			e.rows[id] = domain.Execution{ID: id, Status: st, StartedAt: clk.Now().Add(-age)}
		}
	}
	return e
}

func deps(e *execs, b *blobs, l leader, clk *servicetest.Clock) retention.Deps {
	return retention.Deps{Executions: e, Blobs: b, Leader: l, Clock: clk, Sleeper: sleeper{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestPrunesPerStatusAgeInBatches(t *testing.T) {
	clk := servicetest.NewClock()
	day := 24 * time.Hour
	c := cfg() // success 14d, error 30d, batch 2
	e := fixture(t, clk, map[domain.ExecutionStatus][]time.Duration{
		domain.StatusSuccess: {20 * day, 21 * day, 22 * day, day},
		domain.StatusError:   {20 * day, 40 * day},
		domain.StatusRunning: {90 * day},
	})
	b := &blobs{}
	n, err := retention.New(c, deps(e, b, true, clk)).RunOnce(context.Background())
	if err != nil || n != 4 { // 3 old successes (two rounds) + 1 old error
		t.Fatalf("RunOnce = %d, %v; want 4", n, err)
	}
	if len(e.rows) != 3 || len(b.deleted) != 4 || b.swept != 1 {
		t.Fatalf("rows = %d, blobs deleted = %d, sweeps = %d", len(e.rows), len(b.deleted), b.swept)
	}
}

func TestZeroMaxAgeKeepsStatus(t *testing.T) {
	clk := servicetest.NewClock()
	c := cfg()
	c.MaxAge.Success = 0
	e := fixture(t, clk, map[domain.ExecutionStatus][]time.Duration{domain.StatusSuccess: {1000 * time.Hour}})
	if n, err := retention.New(c, deps(e, &blobs{}, true, clk)).RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("RunOnce = %d, %v; want 0", n, err)
	}
}

func TestFollowerDoesNotPrune(t *testing.T) {
	clk := servicetest.NewClock()
	e := fixture(t, clk, map[domain.ExecutionStatus][]time.Duration{domain.StatusSuccess: {1000 * time.Hour}})
	b := &blobs{}
	if n, err := retention.New(cfg(), deps(e, b, false, clk)).RunOnce(context.Background()); err != nil || n != 0 || len(e.rows) != 1 || b.swept != 0 {
		t.Fatalf("follower pruned: n=%d err=%v rows=%d sweeps=%d", n, err, len(e.rows), b.swept)
	}
}

func TestLoopRunsPassPerInterval(t *testing.T) {
	clk := servicetest.NewClock()
	e := fixture(t, clk, map[domain.ExecutionStatus][]time.Duration{domain.StatusSuccess: {1000 * time.Hour}})
	b := &blobs{}
	d := deps(e, b, true, clk)
	s := sleeper{ticks: make(chan struct{})}
	d.Sleeper = s
	svc := retention.New(cfg(), d)
	svc.Start(context.Background())
	s.ticks <- struct{}{} // one interval elapsed
	s.ticks <- struct{}{} // returns only once the first pass finished
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.rows) != 0 {
		t.Fatalf("rows left = %d", len(e.rows))
	}
}

func TestDisabledStartsNothing(t *testing.T) {
	c := cfg()
	c.Enabled = false
	svc := retention.New(c, retention.Deps{})
	svc.Start(context.Background())
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
