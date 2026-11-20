package credential_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
)

func (e *env) oauthClientCreds(style string) domain.CredentialID {
	e.t.Helper()
	v, err := e.svc.Create(context.Background(), credential.Input{Name: "oa", Type: "engi.oauth2", Data: map[string]any{
		"grantType": "clientCredentials", "accessTokenUrl": e.token.URL, "clientId": "cid",
		"clientSecret": "csecret", "scope": "read write", "authentication": style,
	}})
	if err != nil {
		e.t.Fatal(err)
	}
	return v.ID
}

func TestOAuth2ClientCredentialsFetchCacheRefresh(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.oauthClientCreds("header")

	r1, err := e.svc.Resolve(ctx, id)
	if err != nil || r1.Data["accessToken"] != "access-1" {
		t.Fatalf("first resolve: %v %v", r1.Data, err)
	}
	if _, leaked := r1.Data["oauthTokenData"]; leaked {
		t.Fatal("managed token set must not reach nodes")
	}
	if e.token.basic[0] != "cid:csecret" || e.token.lastForm()["scope"] != "read write" || e.token.lastForm()["grant_type"] != "client_credentials" {
		t.Fatalf("token request wrong: basic=%v form=%v", e.token.basic, e.token.lastForm())
	}
	// Cached: no second token request.
	if r, _ := e.svc.Resolve(ctx, id); r.Data["accessToken"] != "access-1" || e.token.calls.Load() != 1 {
		t.Fatalf("expected cache hit, calls=%d", e.token.calls.Load())
	}
	// Within the refresh skew of expiry: refreshed.
	e.clk.Advance(time.Hour - e.cfg.OAuth2RefreshSkew/2)
	if r, err := e.svc.Resolve(ctx, id); err != nil || r.Data["accessToken"] != "access-2" {
		t.Fatalf("refresh: %v %v", r.Data, err)
	}
	// Token is encrypted at rest and never in the redacted view.
	row, _ := e.store.Credentials().Get(ctx, id)
	if bytes.Contains(row.Data, []byte("access-2")) {
		t.Fatal("token stored in plaintext")
	}
	v, _ := e.svc.Get(ctx, id)
	if _, ok := v.Data["oauthTokenData"]; ok || v.Data["clientSecret"] != credential.Sentinel {
		t.Fatalf("view leaks: %v", v.Data)
	}
	// Editing a token-relevant setting drops the cached token.
	if _, err := e.svc.Update(ctx, id, credential.Input{Name: "oa", Data: map[string]any{
		"grantType": "clientCredentials", "accessTokenUrl": e.token.URL, "clientId": "cid2", "clientSecret": credential.Sentinel}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Resolve(ctx, id); r.Data["accessToken"] != "access-3" {
		t.Fatalf("token should be refetched after edit: %v", r.Data)
	}
}

func TestOAuth2BodyAuthStyle(t *testing.T) {
	e := newEnv(t)
	id := e.oauthClientCreds("body")
	if _, err := e.svc.Resolve(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f := e.token.lastForm()
	if f["client_id"] != "cid" || f["client_secret"] != "csecret" || e.token.basic[0] != ":" {
		t.Fatalf("form=%v basic=%v", f, e.token.basic)
	}
}

func TestOAuth2ConcurrentRefreshIsCoalesced(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.oauthClientCreds("header")
	var wg sync.WaitGroup
	tokens := make([]any, 24)
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.svc.Resolve(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			tokens[i] = r.Data["accessToken"]
		}()
	}
	wg.Wait()
	if n := e.token.calls.Load(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
	for _, tk := range tokens {
		if tk != "access-1" {
			t.Fatalf("tokens = %v", tokens)
		}
	}
	// And again after expiry.
	e.clk.Advance(2 * time.Hour)
	for range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.svc.Resolve(ctx, id)
		}()
	}
	wg.Wait()
	if n := e.token.calls.Load(); n != 2 {
		t.Fatalf("token endpoint called %d times after expiry, want 2", n)
	}
}

func TestOAuth2TokenEndpointFailureIsReported(t *testing.T) {
	e := newEnv(t)
	id := e.oauthClientCreds("header")
	e.token.fail.Store("invalid_client")
	_, err := e.svc.Resolve(context.Background(), id)
	if err == nil || errors.Is(err, domain.ErrNeedsReauth) {
		t.Fatalf("err = %v", err)
	}
	e.token.fail.Store("")
	if _, err := e.svc.Resolve(context.Background(), id); err != nil {
		t.Fatalf("recovers after endpoint heals: %v", err)
	}
}

func (e *env) oauthAuthCode() domain.CredentialID {
	e.t.Helper()
	v, err := e.svc.Create(context.Background(), credential.Input{Name: "ac", Type: "engi.oauth2", Data: map[string]any{
		"grantType": "authorizationCode", "authUrl": "https://provider.test/authorize", "accessTokenUrl": e.token.URL,
		"clientId": "cid", "clientSecret": "csecret", "scope": "a b",
	}})
	if err != nil {
		e.t.Fatal(err)
	}
	return v.ID
}

func TestOAuth2AuthorizationCodePKCE(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.oauthAuthCode()

	// Not connected yet.
	if _, err := e.svc.Resolve(ctx, id); !errors.Is(err, domain.ErrNeedsReauth) {
		t.Fatalf("unconnected resolve err = %v", err)
	}
	authURL, err := e.svc.BeginAuthorization(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Host != "provider.test" || q.Get("response_type") != "code" || q.Get("client_id") != "cid" ||
		q.Get("redirect_uri") != e.cfg.OAuth2RedirectURL || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "a b" {
		t.Fatalf("auth url = %s", authURL)
	}
	state := q.Get("state")

	got, err := e.svc.CompleteAuthorization(ctx, state, "the-code")
	if err != nil || got != id {
		t.Fatalf("complete = %v %v", got, err)
	}
	f := e.token.lastForm()
	sum := sha256.Sum256([]byte(f["code_verifier"]))
	if f["grant_type"] != "authorization_code" || f["code"] != "the-code" ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") || f["redirect_uri"] != e.cfg.OAuth2RedirectURL {
		t.Fatalf("exchange form = %v", f)
	}
	r, err := e.svc.Resolve(ctx, id)
	if err != nil || r.Data["accessToken"] != "access-1" {
		t.Fatalf("resolve after connect: %v %v", r.Data, err)
	}
	// Expiry: refresh_token grant, rotated refresh token is kept.
	e.clk.Advance(2 * time.Hour)
	if r, err = e.svc.Resolve(ctx, id); err != nil || r.Data["accessToken"] != "access-2" {
		t.Fatalf("refresh: %v %v", r.Data, err)
	}
	if f := e.token.lastForm(); f["grant_type"] != "refresh_token" || f["refresh_token"] != "refresh-1" {
		t.Fatalf("refresh form = %v", f)
	}
	e.clk.Advance(2 * time.Hour)
	_, _ = e.svc.Resolve(ctx, id)
	if f := e.token.lastForm(); f["refresh_token"] != "refresh-2" {
		t.Fatalf("rotated refresh token not used: %v", f)
	}
	// Provider revokes the grant: needs re-authorization, flagged on the view.
	e.clk.Advance(2 * time.Hour)
	e.token.fail.Store("invalid_grant")
	if _, err = e.svc.Resolve(ctx, id); !errors.Is(err, domain.ErrNeedsReauth) {
		t.Fatalf("err = %v", err)
	}
	if v, _ := e.svc.Get(ctx, id); !v.NeedsReauth {
		t.Fatal("view must flag needsReauth")
	}
	calls := e.token.calls.Load()
	if _, err = e.svc.Resolve(ctx, id); !errors.Is(err, domain.ErrNeedsReauth) || e.token.calls.Load() != calls {
		t.Fatalf("flagged credential must not hit the provider again: %v", err)
	}
}

func TestOAuth2StateRejections(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.oauthAuthCode()
	authURL, _ := e.svc.BeginAuthorization(ctx, id)
	u, _ := url.Parse(authURL)
	state := u.Query().Get("state")

	// Flip one character so the tampered state always differs from the real one.
	mid, flip := len(state)/2, byte('A')
	if state[mid] == flip {
		flip = 'B'
	}
	tampered := state[:mid] + string(flip) + state[mid+1:]
	for name, st := range map[string]string{"garbage": "nope", "tampered": tampered, "empty": ""} {
		if _, err := e.svc.CompleteAuthorization(ctx, st, "c"); !errors.Is(err, credential.ErrInvalidState) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := e.svc.CompleteAuthorization(ctx, state, ""); !errors.Is(err, credential.ErrInvalidState) {
		t.Errorf("empty code: %v", err)
	}
	e.clk.Advance(e.cfg.OAuth2StateTTL + time.Second)
	if _, err := e.svc.CompleteAuthorization(ctx, state, "c"); !errors.Is(err, credential.ErrInvalidState) {
		t.Errorf("expired: %v", err)
	}
	if e.token.calls.Load() != 0 {
		t.Fatal("rejected states must not reach the token endpoint")
	}
	// Begin on a client-credentials credential is a validation error.
	if _, err := e.svc.BeginAuthorization(ctx, e.oauthClientCreds("header")); err == nil {
		t.Fatal("begin on client credentials must fail")
	}
}
