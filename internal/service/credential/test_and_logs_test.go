package credential_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/credtype/registry"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestTestAction(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	basic, _ := e.svc.Create(ctx, credential.Input{Name: "b", Type: "engi.http.basic", Data: map[string]any{"user": "u"}})
	if err := e.svc.Test(ctx, basic.ID); !errors.Is(err, domain.ErrNoTest) {
		t.Fatalf("err = %v", err)
	}
	// OAuth2 test fetches a token.
	if err := e.svc.Test(ctx, e.oauthClientCreds("header")); err != nil || e.token.calls.Load() != 1 {
		t.Fatalf("oauth test: %v calls=%d", err, e.token.calls.Load())
	}
	e.token.fail.Store("invalid_client")
	if err := e.svc.Test(ctx, e.oauthClientCreds("header")); err == nil {
		t.Fatal("failing token endpoint must fail the test")
	}
}

func TestTestRequestUsesAuthenticate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api")
		if gotKey != "good" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	reg := registry.New()
	if err := reg.Register(nodesdk.CredentialType{
		Name: "test.api", Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "url", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "key", Type: nodesdk.PropString, Required: true}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthHeader, Name: "X-Api", ValueField: "key"},
		Test:         &nodesdk.TestSpec{URLField: "url"},
	}); err != nil {
		t.Fatal(err)
	}
	svc := e.serviceWithTypes(reg)
	good, _ := svc.Create(ctx, credential.Input{Name: "g", Type: "test.api", Data: map[string]any{"url": srv.URL, "key": "good"}})
	bad, _ := svc.Create(ctx, credential.Input{Name: "b", Type: "test.api", Data: map[string]any{"url": srv.URL, "key": "bad"}})
	if err := svc.Test(ctx, good.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Test(ctx, bad.ID); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

// TestSecretsNeverReachLogs runs every operation with debug logging and checks
// that no secret value appears in the captured output.
func TestSecretsNeverReachLogs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	secrets := []string{"hunter2", "csecret", "access-1", "access-2", "refresh-1", "the-code", "new-pass"}

	v, _ := e.svc.Create(ctx, credential.Input{Name: "b", Type: "engi.http.basic", Data: map[string]any{"user": "u", "password": "hunter2"}})
	_, _ = e.svc.Update(ctx, v.ID, credential.Input{Name: "b", Data: map[string]any{"user": "u", "password": "new-pass"}})
	_, _ = e.svc.Resolve(ctx, v.ID)
	_, _ = e.svc.Create(ctx, credential.Input{Name: "bad", Type: "engi.http.basic", Data: map[string]any{"password": "hunter2", "bogus": "hunter2"}})
	_ = e.svc.Test(ctx, v.ID)

	cc := e.oauthClientCreds("header")
	_, _ = e.svc.Resolve(ctx, cc)
	ac := e.oauthAuthCode()
	authURL, _ := e.svc.BeginAuthorization(ctx, ac)
	state := authURL[strings.Index(authURL, "state=")+6:]
	state = strings.SplitN(state, "&", 2)[0]
	_, _ = e.svc.CompleteAuthorization(ctx, state, "the-code")
	e.clk.Advance(2 * time.Hour)
	_, _ = e.svc.Resolve(ctx, ac)
	e.token.fail.Store("invalid_grant")
	e.clk.Advance(2 * time.Hour)
	_, _ = e.svc.Resolve(ctx, ac)
	_, _ = e.svc.RotateKeys(ctx)
	_ = e.svc.Delete(ctx, v.ID)

	out := e.logs.String()
	if !strings.Contains(out, "credential created") {
		t.Fatalf("expected log output, got %q", out)
	}
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("log output contains secret %q", s)
		}
	}
}
