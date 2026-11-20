package credential_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/crypto/aesgcm"
	"github.com/engi-hq/engi/internal/adapter/oauth2/httptoken"
	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/credential"
)

// keyedLocker stands in for the cluster-wide lock: one mutex per key, shared
// by every service built on it, as if the services were replicas. When gate
// is set, each caller waits there until all of them have asked for the lock,
// so they queue on it together.
type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	gate  *sync.WaitGroup
}

func newKeyedLocker() *keyedLocker { return &keyedLocker{locks: map[string]*sync.Mutex{}} }

func (k *keyedLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	k.mu.Lock()
	m := k.locks[key]
	if m == nil {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	gate := k.gate
	k.mu.Unlock()
	if gate != nil {
		gate.Done()
		gate.Wait()
	}
	m.Lock()
	defer m.Unlock()
	return fn(ctx)
}

var _ port.KeyLocker = (*keyedLocker)(nil)

// replica builds a service over the env's store that takes refresh locks from locker.
func (e *env) replica(locker port.KeyLocker) *credential.Service {
	e.t.Helper()
	cipher, err := aesgcm.New(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		e.t.Fatal(err)
	}
	svc, err := credential.New(e.cfg, credential.Deps{
		Store: e.store.Credentials(), Cipher: cipher, Types: reg, Clock: e.clk, Tx: e.store,
		IDs:         id.NewGenerator(e.clk.Now, rand.Reader),
		OAuth2:      httptoken.New(http.DefaultClient, e.cfg.OAuth2RequestTimeout),
		HTTP:        http.DefaultClient,
		RefreshLock: locker,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}

// TestOAuth2RefreshAcrossReplicasRereadsAfterLock: two replicas find the token
// expired and queue on the refresh lock. The second one must re-read the
// token the first stored and reuse it, so the token endpoint sees one refresh.
func TestOAuth2RefreshAcrossReplicasRereadsAfterLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	cred := e.oauthClientCreds("header")
	if _, err := e.svc.Resolve(ctx, cred); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(2 * time.Hour) // expired

	locker := newKeyedLocker()
	locker.gate = &sync.WaitGroup{}
	locker.gate.Add(2)
	a, b := e.replica(locker), e.replica(locker)

	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i, svc := range []*credential.Service{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Resolve(ctx, cred)
			errs[i] = err
			got[i], _ = r.Data["accessToken"].(string)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got[0] != got[1] || got[0] != "access-2" {
		t.Fatalf("tokens = %v, want both access-2", got)
	}
	// One initial fetch plus exactly one refresh.
	if n := e.token.calls.Load(); n != 2 {
		t.Fatalf("token endpoint called %d times, want 2", n)
	}
}

// TestOAuth2RefreshRefusalIsStored: a refused refresh marks the credential
// as needing re-authorization, and the mark survives the lock section.
func TestOAuth2RefreshRefusalIsStored(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	cred := e.oauthClientCreds("header")
	svc := e.replica(newKeyedLocker())
	if _, err := svc.Resolve(ctx, cred); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(2 * time.Hour)
	e.token.fail.Store("invalid_grant")

	_, err := svc.Resolve(ctx, cred)
	if !errors.Is(err, domain.ErrNeedsReauth) {
		t.Fatalf("err = %v, want ErrNeedsReauth", err)
	}
	v, err := svc.Get(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	if !v.NeedsReauth {
		t.Fatal("refusal was not stored: credential does not need reauth")
	}
}
