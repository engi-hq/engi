//go:build integration

package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/crypto/aesgcm"
	pglock "github.com/engi-hq/engi/internal/adapter/lock/postgres"
	"github.com/engi-hq/engi/internal/adapter/pgdb/pgtest"
	pgstore "github.com/engi-hq/engi/internal/adapter/store/postgres"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
	"github.com/engi-hq/engi/internal/service/credential"
)

// countingTokens is a token endpoint that is slow enough for the second
// replica to reach the lock while the first one refreshes.
type countingTokens struct {
	calls atomic.Int32
}

func (c *countingTokens) Token(ctx context.Context, _ port.OAuth2TokenRequest) (port.OAuth2Token, error) {
	n := c.calls.Add(1)
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return port.OAuth2Token{}, ctx.Err()
	}
	return port.OAuth2Token{AccessToken: fmt.Sprintf("access-%d", n), ExpiresIn: time.Hour}, nil
}

// TestRefreshAcrossReplicasOnPostgres runs two credential services, each on its
// own pool, over the real Postgres store and lock. Only one refresh may reach
// the token endpoint; the replica that waited reads the stored token.
func TestRefreshAcrossReplicasOnPostgres(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	clk := porttest.NewFakeClock()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := config.Default().Credentials
	cfg.ActiveKeyID = "k1"
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg.Keys = map[string]config.Secret{"k1": config.Secret(base64.StdEncoding.EncodeToString(key))}
	cipher, err := aesgcm.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		t.Fatal(err)
	}
	tokens := &countingTokens{}
	newReplica := func() *credential.Service {
		db, err := env.Target.Open(ctx, env.Store)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc, err := credential.New(cfg, credential.Deps{
			Store: pgstore.New(db, env.Store, clk).Credentials(), Cipher: cipher, Types: reg, Clock: clk,
			OAuth2: tokens, IDs: id.NewGenerator(clk.Now, rand.Reader),
			RefreshLock: pglock.New(db, cfg.OAuth2RefreshLockTimeout), Logger: logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}
	a, b := newReplica(), newReplica()

	v, err := a.Create(ctx, credential.Input{Name: "oa", Type: "engi.oauth2", Data: map[string]any{
		"grantType": "clientCredentials", "accessTokenUrl": "https://provider.test/token",
		"clientId": "cid", "clientSecret": "csecret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Resolve(ctx, v.ID); err != nil { // first token, not expired
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour) // expired: both replicas will refresh

	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i, svc := range []*credential.Service{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Resolve(ctx, v.ID)
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
	if n := tokens.calls.Load(); n != 2 {
		t.Fatalf("token endpoint called %d times, want 2 (initial + one refresh)", n)
	}
}
