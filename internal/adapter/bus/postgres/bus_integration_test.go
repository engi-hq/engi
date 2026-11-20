//go:build integration

package postgres_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/bus/postgres"
	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

const wait = 5 * time.Second

func newBus(t *testing.T, env *pgtest.Env) *postgres.Bus {
	t.Helper()
	b, err := postgres.New(env.DB, env.Target, config.Default().Bus, 16, 100*time.Millisecond, systemClock{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func recv(t *testing.T, s port.Subscription) port.Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatal("subscription closed")
		}
		return e
	case <-time.After(wait):
		t.Fatal("no event received")
	}
	return port.Event{}
}

func TestEventsCrossBusInstances(t *testing.T) {
	env := pgtest.New(t)
	pubBus, subBus := newBus(t, env), newBus(t, env)
	ctx := context.Background()
	sub, err := subBus.Subscribe(ctx, port.EventFilter{ExecutionID: "ex_1", Kinds: []port.EventKind{port.EventCancelRequested, port.EventExecutionFinished}})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	at := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	// Filtered out: another execution, another kind.
	_ = pubBus.Publish(ctx, port.Event{Kind: port.EventCancelRequested, ExecutionID: "ex_2", At: at})
	_ = pubBus.Publish(ctx, port.Event{Kind: port.EventNodeStarted, ExecutionID: "ex_1", At: at})
	want := port.Event{Kind: port.EventExecutionFinished, ExecutionID: "ex_1", WorkflowID: "wf_1", At: at,
		Status: domain.StatusError, Error: &domain.ErrorInfo{Message: "boom"}}
	if err := pubBus.Publish(ctx, want); err != nil {
		t.Fatal(err)
	}
	got := recv(t, sub)
	if got.Kind != want.Kind || got.ExecutionID != "ex_1" || got.Status != domain.StatusError || got.Error == nil || got.Error.Message != "boom" || !got.At.Equal(at) {
		t.Fatalf("got %+v", got)
	}
}

func TestLargeEventsGoThroughTheTable(t *testing.T) {
	env := pgtest.New(t)
	b := newBus(t, env)
	ctx := context.Background()
	sub, err := b.Subscribe(ctx, port.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	big := strings.Repeat("x", 50_000)
	run := domain.NodeRun{Error: &domain.ErrorInfo{Message: big}}
	if err := b.Publish(ctx, port.Event{Kind: port.EventNodeFinished, ExecutionID: "ex_1", Node: "n", Run: &run, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got := recv(t, sub)
	if got.Run == nil || got.Run.Error == nil || got.Run.Error.Message != big {
		t.Fatal("large node run did not arrive intact")
	}
	var stored int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM bus_events`).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored events = %d, %v; want 1", stored, err)
	}
}

func TestListenerReconnectsAfterConnectionLoss(t *testing.T) {
	env := pgtest.New(t)
	b := newBus(t, env)
	ctx := context.Background()
	sub, err := b.Subscribe(ctx, port.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	// Drop the listener's connection (and the pool's idle ones) and keep
	// publishing until an event gets through again.
	env.Terminate(t, "")
	deadline := time.After(wait)
	for {
		_ = b.Publish(ctx, port.Event{Kind: port.EventExecutionStarted, ExecutionID: "ex_1", At: time.Now()})
		select {
		case <-sub.Events():
			return
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("bus did not recover after the listener connection was killed")
		}
	}
}
