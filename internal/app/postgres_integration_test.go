//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// podPair is two runtimes ("pods") on one Postgres database.
type podPair struct {
	env  *pgtest.Env
	a, b *runtimeFixture
}

func newPods(t *testing.T) podPair {
	t.Helper()
	env := pgtest.New(t)
	mutate := func(c *config.Config) {
		c.Store = env.Store
		c.Trigger.Lease.RetryPeriod, c.Trigger.Lease.RenewDeadline = 100*time.Millisecond, 2*time.Second
		c.Nodes.Code.Timeout = time.Minute
	}
	return podPair{env: env, a: newRuntime(t, mutate), b: newRuntime(t, mutate)}
}

func TestPodsExecuteEveryJobExactlyOnce(t *testing.T) {
	p := newPods(t)
	for _, pod := range []*runtimeFixture{p.a, p.b} {
		if got := pod.app.Config.ResolveQueueDriver() + pod.app.Config.ResolveBusDriver() + pod.app.Config.ResolveLeaseType(); got != "postgrespostgrespostgres" {
			t.Fatalf("auto drivers resolved to %q", got)
		}
	}
	// Both pods see every execution.started on the shared bus.
	sub, err := p.a.rt.Bus.Subscribe(t.Context(), port.EventFilter{Kinds: []port.EventKind{port.EventExecutionStarted}})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	p.a.start(t)
	p.b.start(t)

	wf := p.a.save(t, pipelineDoc)
	const n = 40
	ids := make([]domain.ExecutionID, n)
	for i := range ids {
		pod := p.a
		if i%2 == 1 {
			pod = p.b
		}
		ids[i] = pod.begin(t, wf, "Start", items(1, 5, 2)...)
	}
	for _, id := range ids {
		ex := p.a.status(t, id, domain.StatusSuccess)
		if runs := ex.RunData["Start"]; len(runs) != 1 {
			t.Fatalf("execution %s: Start ran %d times, want once", id, len(runs))
		}
	}
	started := map[domain.ExecutionID]int{}
	deadline := time.After(5 * time.Second)
	for len(started) < n {
		select {
		case e := <-sub.Events():
			started[e.ExecutionID]++
		case <-deadline:
			t.Fatalf("saw execution.started for %d of %d executions", len(started), n)
		}
	}
	time.Sleep(200 * time.Millisecond) // a duplicate run would announce itself late
	for {
		select {
		case e := <-sub.Events():
			started[e.ExecutionID]++
			continue
		default:
		}
		break
	}
	for id, c := range started {
		if c != 1 {
			t.Errorf("execution %s started %d times, want exactly once", id, c)
		}
	}
	var jobs int
	if err := p.env.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs left = %d (%v)", jobs, err)
	}
}

func TestCancelPublishedOnOnePodReachesTheOther(t *testing.T) {
	p := newPods(t)
	p.b.start(t) // only B works; A has no worker, so the run is on B
	spin := p.a.save(t, `{"name": "spin", "nodes": [
	  {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
	  {"name": "Spin", "type": "engi.code.javascript", "position": [200, 0], "parameters": {"jsCode": "while (true) {}"}}
	], "connections": [{"from": {"node": "Start"}, "to": {"node": "Spin"}}]}`)
	id := p.a.begin(t, spin, "Start", items(1)...)
	p.b.status(t, id, domain.StatusRunning)
	if err := p.a.rt.Executions.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if ex := p.b.status(t, id, domain.StatusCanceled); ex.Error == nil || ex.Error.Kind != domain.ErrorKindCanceled {
		t.Fatalf("execution = %+v", ex)
	}
}

// leaseHolders returns the backend pids holding the leader advisory lock.
func leaseHolders(t *testing.T, p podPair) []int {
	t.Helper()
	rows, err := p.env.DB.Query(`SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted AND classid = 0 AND objid = $1`,
		p.a.app.Config.Trigger.Lease.LockKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pids []int
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

func waitHolders(t *testing.T, p podPair, what string, ok func([]int) bool) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if h := leaseHolders(t, p); ok(h) {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; holders = %v", what, leaseHolders(t, p))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestOnlyOnePodHoldsTheLeaseAndTheOtherTakesOver(t *testing.T) {
	p := newPods(t)
	ctx := context.Background()
	if err := p.a.rt.Triggers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	first := waitHolders(t, p, "pod A to lead", func(h []int) bool { return len(h) == 1 })
	if err := p.b.rt.Triggers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // B campaigns repeatedly meanwhile
	if h := leaseHolders(t, p); len(h) != 1 || h[0] != first[0] {
		t.Fatalf("holders after B joined = %v, want only A's %v", h, first)
	}
	if err := p.a.rt.Triggers.Shutdown(ctx); err != nil { // A releases cleanly
		t.Fatal(err)
	}
	waitHolders(t, p, "pod B to take over", func(h []int) bool { return len(h) == 1 && h[0] != first[0] })
}
