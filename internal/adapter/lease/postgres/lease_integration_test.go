//go:build integration

package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/lease/postgres"
	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

const wait = 10 * time.Second

// replica is one campaigning provider with observable leadership.
type replica struct {
	leading atomic.Bool
	started atomic.Int32
	cancel  context.CancelFunc
	done    chan struct{}
}

func startReplica(t *testing.T, env *pgtest.Env) *replica {
	t.Helper()
	cfg := config.Default().Trigger.Lease
	cfg.RetryPeriod, cfg.RenewDeadline = 100*time.Millisecond, 2*time.Second
	p := postgres.New(env.Target, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	r := &replica{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		_ = p.Run(ctx, port.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				r.leading.Store(true)
				r.started.Add(1)
				<-ctx.Done()
			},
			OnStoppedLeading: func() { r.leading.Store(false) },
		})
	}()
	t.Cleanup(func() { cancel(); <-r.done })
	return r
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOneLeaderAndTakeoverAfterRelease(t *testing.T) {
	env := pgtest.New(t)
	a := startReplica(t, env)
	eventually(t, "first replica to lead", a.leading.Load)
	b := startReplica(t, env)
	time.Sleep(500 * time.Millisecond)
	if b.leading.Load() {
		t.Fatal("two replicas lead at once")
	}
	a.cancel() // clean shutdown releases the lock
	<-a.done
	if a.leading.Load() {
		t.Fatal("OnStoppedLeading must have run before Run returned")
	}
	eventually(t, "second replica to take over", b.leading.Load)
}

func TestLeaderReacquiresAfterConnectionLoss(t *testing.T) {
	env := pgtest.New(t)
	a := startReplica(t, env)
	eventually(t, "leading", a.leading.Load)
	// Kill every backend; the pool is unaffected as it reconnects on demand.
	env.Terminate(t, "")
	eventually(t, "a second tenure after the connection loss", func() bool { return a.started.Load() >= 2 && a.leading.Load() })
}
