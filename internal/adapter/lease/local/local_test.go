package local

import (
	"context"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/port"
)

func TestRunLeadsUntilContextEnds(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New().Run(ctx, port.LeaderCallbacks{
			OnStartedLeading: func(lctx context.Context) {
				close(started)
				<-lctx.Done()
			},
			OnStoppedLeading: func() { close(stopped) },
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("never became leader")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("OnStoppedLeading not called")
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
