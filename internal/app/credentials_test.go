package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/credential/noop"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/credential"
)

func TestNoKeyringKeepsCredentialsDisabled(t *testing.T) {
	a := newApp(t)
	if a.Credentials != nil {
		t.Fatal("credential service must be nil without a keyring")
	}
	if _, err := (noop.Resolver{}).Resolve(context.Background(), "cr_x"); err == nil {
		t.Fatal("noop resolver must not resolve")
	}
	if a.CredentialTypes == nil || len(a.CredentialTypes.List()) == 0 {
		t.Fatal("credential types should still be registered")
	}
}

func TestKeyringWiresCredentialsIntoTheEngine(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg := config.Default()
	cfg.Store.Path = filepath.Join(t.TempDir(), "engi.db")
	cfg.Credentials.ActiveKeyID = "k1"
	cfg.Credentials.Keys = map[string]config.Secret{"k1": config.Secret(base64.StdEncoding.EncodeToString(key))}
	var logs bytes.Buffer
	a, err := New(t.Context(), cfg, Options{LogOutput: &logs, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseCredentials() })
	if a.Credentials == nil {
		t.Fatal("credential service missing")
	}
	view, err := a.Credentials.Create(context.Background(), credential.Input{
		Name: "api", Type: "engi.http.bearer", Data: map[string]any{"token": "s3cr3t-token"}})
	if err != nil {
		t.Fatal(err)
	}
	wf := workflow(t, fmt.Sprintf(`{
	  "name": "auth", "nodes": [
	    {"id": "nd_01J00000000000000000000001", "name": "Start", "type": "engi.trigger.manual", "typeVersion": 1},
	    {"id": "nd_01J00000000000000000000002", "name": "Call", "type": "engi.http.request", "typeVersion": 1,
	     "parameters": {"url": %q, "authentication": "generic", "genericAuthType": "engi.http.bearer"},
	     "credentials": {"engi.http.bearer": %q}}
	  ],
	  "connections": [{"from": {"node": "Start", "port": "main", "index": 0}, "to": {"node": "Call", "port": "main", "index": 0}}]
	}`, srv.URL, view.ID))
	state, err := a.Run(context.Background(), RunRequest{Workflow: wf})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != domain.StatusSuccess {
		t.Fatalf("status = %s, error = %+v", state.Status, state.Error)
	}
	if gotAuth != "Bearer s3cr3t-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if bytes.Contains(logs.Bytes(), []byte("s3cr3t-token")) {
		t.Fatal("token leaked into logs")
	}
}

// keyringApp builds an App with a keyring and a SQLite store in a temporary
// directory, the configuration under which the credential service needs the
// database.
func keyringApp(t *testing.T) *App {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg := config.Default()
	cfg.Store.Path = filepath.Join(t.TempDir(), "engi.db")
	cfg.Credentials.ActiveKeyID = "k1"
	cfg.Credentials.Keys = map[string]config.Secret{"k1": config.Secret(base64.StdEncoding.EncodeToString(key))}
	var logs bytes.Buffer
	a, err := New(t.Context(), cfg, Options{LogOutput: &logs, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseCredentials() })
	return a
}

// TestRuntimeReusesCredentialDatabase guards against a second connection pool
// on the same database file: the credential service and the runtime must run
// on the one handle the App opened.
func TestRuntimeReusesCredentialDatabase(t *testing.T) {
	a := keyringApp(t)
	if a.Credentials == nil || a.db == nil {
		t.Fatal("keyring app should open the store once, for credentials")
	}
	opened := a.db
	rt, err := a.NewRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rt.db != opened {
		t.Fatal("runtime opened its own database pool instead of sharing the credential one")
	}
	if a.db != opened {
		t.Fatal("building the runtime replaced the shared database handle")
	}
	if err := rt.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestSharedDatabaseCloseIsIdempotent checks that the runtime and the App can
// both release the shared handle: the second close is a no-op, and a runtime
// built afterwards reopens the database instead of using a closed one.
func TestSharedDatabaseCloseIsIdempotent(t *testing.T) {
	a := keyringApp(t)
	rt, err := a.NewRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseCredentials(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if a.db != nil {
		t.Fatal("closed handle still shared with the App")
	}
	rt2, err := a.NewRuntime(t.Context())
	if err != nil {
		t.Fatalf("runtime after close: %v", err)
	}
	if err := rt2.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}
