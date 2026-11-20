package credential_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/crypto/aesgcm"
	"github.com/engi-hq/engi/internal/adapter/oauth2/httptoken"
	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/adapter/store/sqlite"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port/porttest"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/migrations"
)

// env wires a credential service to a real SQLite store, a real AES-GCM
// cipher and a token endpoint served by httptest.
type env struct {
	t     *testing.T
	svc   *credential.Service
	store *sqlite.Store
	cfg   config.CredentialsConfig
	clk   *porttest.FakeClock
	logs  *bytes.Buffer
	token *tokenServer
}

func newKey() config.Secret {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return config.Secret(base64.StdEncoding.EncodeToString(b))
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	scfg := config.Default().Store
	scfg.Path = filepath.Join(t.TempDir(), "engi.db")
	db, err := sqlitedb.Open(ctx, scfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitedb.Migrate(ctx, db, migrations.SQLite, migrations.SQLiteDir); err != nil {
		t.Fatal(err)
	}
	clk := porttest.NewFakeClock()
	e := &env{t: t, store: sqlite.New(db, scfg, clk), clk: clk, logs: &bytes.Buffer{}, token: newTokenServer(t)}
	e.cfg = config.Default().Credentials
	e.cfg.ActiveKeyID = "k1"
	e.cfg.Keys = map[string]config.Secret{"k1": newKey()}
	e.cfg.OAuth2RedirectURL = "https://engi.test/oauth2/callback"
	e.svc = e.service(e.cfg)
	return e
}

// service builds a service for cfg sharing this env's store.
func (e *env) service(cfg config.CredentialsConfig) *credential.Service {
	e.t.Helper()
	cipher, err := aesgcm.New(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	reg, err := builtin.Registry()
	if err != nil {
		e.t.Fatal(err)
	}
	svc, err := credential.New(cfg, credential.Deps{
		Store: e.store.Credentials(), Cipher: cipher, Types: reg, Clock: e.clk, Tx: e.store,
		IDs:    id.NewGenerator(e.clk.Now, rand.Reader),
		OAuth2: httptoken.New(http.DefaultClient, cfg.OAuth2RequestTimeout),
		HTTP:   http.DefaultClient,
		Logger: slog.New(slog.NewJSONHandler(syncWriter{e.logs, &sync.Mutex{}}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}

type syncWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// tokenServer is a fake OAuth2 token endpoint.
type tokenServer struct {
	*httptest.Server
	calls   atomic.Int32
	counter atomic.Int32
	mu      sync.Mutex
	forms   []map[string]string
	basic   []string // client_id:secret seen in Authorization headers
	// fail, when set, answers with this OAuth2 error.
	fail atomic.Value
	// expiresIn is the lifetime (seconds) granted.
	expiresIn atomic.Int32
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{}
	ts.expiresIn.Store(3600)
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.calls.Add(1)
		_ = r.ParseForm()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		user, pass, _ := r.BasicAuth()
		ts.mu.Lock()
		ts.forms = append(ts.forms, form)
		ts.basic = append(ts.basic, user+":"+pass)
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if msg, _ := ts.fail.Load().(string); msg != "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
			return
		}
		n := ts.counter.Add(1)
		body := map[string]any{"access_token": "access-" + itoa(n), "token_type": "Bearer", "expires_in": ts.expiresIn.Load()}
		if form["grant_type"] != "client_credentials" {
			body["refresh_token"] = "refresh-" + itoa(n)
		}
		writeJSON(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *tokenServer) lastForm() map[string]string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.forms[len(ts.forms)-1]
}

// serviceWithTypes builds a service like service() but with custom types.
func (e *env) serviceWithTypes(types credential.TypeRegistry) *credential.Service {
	e.t.Helper()
	cipher, err := aesgcm.New(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	svc, err := credential.New(e.cfg, credential.Deps{
		Store: e.store.Credentials(), Cipher: cipher, Types: types, Clock: e.clk,
		IDs: id.NewGenerator(e.clk.Now, rand.Reader), HTTP: http.DefaultClient,
		Logger: slog.New(slog.NewJSONHandler(syncWriter{e.logs, &sync.Mutex{}}, nil)),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return svc
}
