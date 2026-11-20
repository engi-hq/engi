//go:build perf

package app

import (
	"context"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/engi-hq/engi/internal/config"
)

// Thresholds for the memory regression guard (run with `go test -tags perf`).
// Measured on the reference setup: ~5 MiB idle, ~8 MiB after the run; the
// limits leave room for noise but fail on a leak or an eager big allocation
// such as hashing a password at startup (64 MiB).
const (
	perfExecutions       = 100
	perfIdleHeapMaxMiB   = 12
	perfActiveHeapMaxMiB = 20
)

// retainedMiB is heap memory the process holds from the OS, garbage included,
// without forcing a collection first. It is what RSS follows.
func retainedMiB() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return (m.HeapSys - m.HeapReleased) >> 20
}

func heapInuseMiB() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse >> 20
}

// TestMemoryStaysBounded boots the real server with production argon2
// parameters, logs in, runs webhook executions and checks the Go heap in use.
func TestMemoryStaysBounded(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Argon2 = config.Default().Auth.Argon2
	a, err := New(t.Context(), cfg, Options{LookupEnv: func(string) (string, bool) { return "", false }, LogOutput: &syncBuffer{}})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := a.NewServer(t.Context(), ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	f := &serverFixture{t: t, app: a, srv: srv, ts: &httptest.Server{URL: "http://" + srv.Addr()}, logs: &syncBuffer{}}

	t.Logf("idle: retained %d MiB", retainedMiB())
	if got := retainedMiB(); got > perfIdleHeapMaxMiB {
		t.Fatalf("idle retained heap = %d MiB, limit %d", got, perfIdleHeapMaxMiB)
	}
	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: webhookWorkflow}).json(t)["id"].(string)
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/activate"}), 200)
	for range perfExecutions {
		wantStatus(t, f.do(req{method: "POST", path: "/hook/ping", body: map[string]any{"a": 1}}), 200)
	}
	t.Logf("after: retained %d MiB, in use after GC %d MiB", retainedMiB(), heapInuseMiB())
	if got := retainedMiB(); got > perfActiveHeapMaxMiB {
		t.Fatalf("retained heap after login and %d executions = %d MiB, limit %d", perfExecutions, got, perfActiveHeapMaxMiB)
	}
	if got := heapInuseMiB(); got > perfActiveHeapMaxMiB {
		t.Fatalf("heap in use after login and %d executions = %d MiB, limit %d", perfExecutions, got, perfActiveHeapMaxMiB)
	}
}
