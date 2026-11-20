package memory_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/bus/memory"
	"github.com/engi-hq/engi/internal/port"
)

func newBus(t *testing.T, buffer int) *memory.Bus {
	t.Helper()
	b, err := memory.New(buffer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func recv(t *testing.T, s port.Subscription) (port.Event, bool) {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		return e, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an event")
		return port.Event{}, false
	}
}

func TestFilteringByExecutionAndKind(t *testing.T) {
	b := newBus(t, 8)
	ctx := context.Background()
	all, _ := b.Subscribe(ctx, port.EventFilter{})
	one, _ := b.Subscribe(ctx, port.EventFilter{ExecutionID: "ex_1"})
	fin, _ := b.Subscribe(ctx, port.EventFilter{Kinds: []port.EventKind{port.EventExecutionFinished}})
	defer all.Close()
	defer one.Close()
	defer fin.Close()

	_ = b.Publish(ctx, port.Event{Kind: port.EventNodeFinished, ExecutionID: "ex_1"})
	_ = b.Publish(ctx, port.Event{Kind: port.EventExecutionFinished, ExecutionID: "ex_2"})

	if e, _ := recv(t, all); e.ExecutionID != "ex_1" {
		t.Fatalf("all got %+v", e)
	}
	if e, _ := recv(t, all); e.ExecutionID != "ex_2" {
		t.Fatalf("all got %+v", e)
	}
	if e, _ := recv(t, one); e.Kind != port.EventNodeFinished {
		t.Fatalf("one got %+v", e)
	}
	if e, _ := recv(t, fin); e.ExecutionID != "ex_2" {
		t.Fatalf("fin got %+v", e)
	}
	select {
	case e := <-one.Events():
		t.Fatalf("one must not see ex_2, got %+v", e)
	default:
	}
}

func TestSlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := newBus(t, 1)
	ctx := context.Background()
	s, _ := b.Subscribe(ctx, port.EventFilter{})
	defer s.Close()
	done := make(chan struct{})
	go func() {
		for range 5 {
			_ = b.Publish(ctx, port.Event{Kind: port.EventNodeStarted})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}

func TestContextEndClosesSubscription(t *testing.T) {
	b := newBus(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := b.Subscribe(ctx, port.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, ok := recv(t, s); ok {
		t.Fatal("channel must be closed")
	}
	s.Close() // idempotent
	if err := b.Publish(context.Background(), port.Event{}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsZeroBuffer(t *testing.T) {
	if _, err := memory.New(0, slog.Default()); err == nil {
		t.Fatal("zero buffer must fail")
	}
}
