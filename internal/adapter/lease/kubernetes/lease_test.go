package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

const leasePath = "/apis/coordination.k8s.io/v1/namespaces/engi/leases"

// fakeAPI is an in-memory Lease endpoint with optimistic concurrency.
type fakeAPI struct {
	mu      sync.Mutex
	obj     *leaseObject
	version int
	auth    string
	fail    atomic.Bool // answer 500 to everything
}

func (f *fakeAPI) holder() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.obj == nil || f.obj.Spec.HolderIdentity == nil {
		return ""
	}
	return *f.obj.Spec.HolderIdentity
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	if f.fail.Load() {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	reply := func(code int) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(f.obj)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == leasePath+"/l":
		if f.obj == nil {
			http.NotFound(w, r)
			return
		}
		reply(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == leasePath:
		if f.obj != nil {
			http.Error(w, "exists", http.StatusConflict)
			return
		}
		f.store(r)
		reply(http.StatusCreated)
	case r.Method == http.MethodPut && r.URL.Path == leasePath+"/l":
		var in leaseObject
		_ = json.NewDecoder(r.Body).Decode(&in)
		if f.obj == nil || in.Metadata.ResourceVersion != f.obj.Metadata.ResourceVersion {
			http.Error(w, "stale", http.StatusConflict)
			return
		}
		f.obj = &in
		f.version++
		f.obj.Metadata.ResourceVersion = strconv.Itoa(f.version)
		reply(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) store(r *http.Request) {
	var in leaseObject
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.version++
	in.Metadata.ResourceVersion = strconv.Itoa(f.version)
	f.obj = &in
}

func newProvider(t *testing.T, srv *httptest.Server, id string) *Provider {
	t.Helper()
	cfg := config.LeaseConfig{
		Name: "l", Namespace: "engi", Identity: id, APIServer: srv.URL, TokenFile: "token",
		Duration: 300 * time.Millisecond, RenewDeadline: 200 * time.Millisecond, RetryPeriod: 20 * time.Millisecond,
	}
	p, err := New(cfg, Options{
		ReadFile: func(string) ([]byte, error) { return []byte("tok\n"), nil },
		Client:   srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type events struct {
	started, stopped atomic.Int32
	leading          atomic.Bool
}

func (e *events) callbacks() port.LeaderCallbacks {
	return port.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			e.started.Add(1)
			e.leading.Store(true)
			<-ctx.Done()
		},
		OnStoppedLeading: func() { e.stopped.Add(1); e.leading.Store(false) },
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCreatesLeaseLeadsAndReleases(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	p := newProvider(t, srv, "pod-a")
	var ev events
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx, ev.callbacks()) }()

	waitFor(t, "leading", ev.leading.Load)
	if api.holder() != "pod-a" {
		t.Fatalf("holder = %q", api.holder())
	}
	if api.auth != "Bearer tok" {
		t.Fatalf("auth header = %q", api.auth)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if ev.stopped.Load() != 1 || api.holder() != "" {
		t.Fatalf("stopped=%d holder=%q, want 1 and released", ev.stopped.Load(), api.holder())
	}
}

func TestStandbyTakesOverAfterRelease(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	var a, b events
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go func() { _ = newProvider(t, srv, "pod-a").Run(ctxA, a.callbacks()) }()
	waitFor(t, "a leading", a.leading.Load)
	go func() { _ = newProvider(t, srv, "pod-b").Run(ctxB, b.callbacks()) }()

	time.Sleep(3 * 300 * time.Millisecond) // well past the lease duration
	if b.leading.Load() || !a.leading.Load() {
		t.Fatalf("leaders: a=%v b=%v, want only a (renewals keep the lease)", a.leading.Load(), b.leading.Load())
	}
	cancelA()
	waitFor(t, "b leading", b.leading.Load)
	if api.holder() != "pod-b" {
		t.Fatalf("holder = %q", api.holder())
	}
	if api.obj.Spec.LeaseTransitions < 1 {
		t.Fatalf("leaseTransitions = %d", api.obj.Spec.LeaseTransitions)
	}
}

func TestTakesOverExpiredLeaseOfCrashedHolder(t *testing.T) {
	api := &fakeAPI{}
	dead := "dead-pod"
	api.obj = &leaseObject{Spec: leaseSpec{HolderIdentity: &dead, LeaseDurationSeconds: 1, RenewTime: "2020-01-01T00:00:00.000000Z"}}
	api.obj.Metadata.Name, api.obj.Metadata.ResourceVersion = "l", "1"
	api.version = 1
	srv := httptest.NewServer(api)
	defer srv.Close()
	var ev events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go func() { _ = newProvider(t, srv, "pod-a").Run(ctx, ev.callbacks()) }()
	waitFor(t, "takeover", ev.leading.Load)
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("took over after %v, before the lease duration elapsed", time.Since(start))
	}
}

func TestStepsDownWhenRenewalFailsPastDeadline(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	var ev events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = newProvider(t, srv, "pod-a").Run(ctx, ev.callbacks()) }()
	waitFor(t, "leading", ev.leading.Load)

	api.fail.Store(true)
	waitFor(t, "step down", func() bool { return ev.stopped.Load() == 1 })
	api.fail.Store(false)
	waitFor(t, "re-acquire", func() bool { return ev.started.Load() == 2 })
}

func TestNewReadsNamespaceFileAndRejectsEmpty(t *testing.T) {
	cfg := config.LeaseConfig{Name: "l", Identity: "x", APIServer: "http://k", NamespaceFile: "ns", TokenFile: "t"}
	read := func(string) ([]byte, error) { return []byte(" prod \n"), nil }
	p, err := New(cfg, Options{ReadFile: read})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.path, "/namespaces/prod/leases") {
		t.Fatalf("path = %s", p.path)
	}
	if _, err := New(cfg, Options{ReadFile: func(string) ([]byte, error) { return []byte("\n"), nil }}); err == nil {
		t.Fatal("empty namespace accepted")
	}
}
