package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
)

const (
	testEmail    = "owner@example.com"
	testPassword = "correct horse battery"
)

// syncBuffer is a log sink that is safe to write from server goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serverFixture is a real server (SQLite, auth, API) behind httptest. The
// worker is not started unless a test asks for it, so executions stay queued.
type serverFixture struct {
	t    *testing.T
	app  *App
	srv  *Server
	ts   *httptest.Server
	logs *syncBuffer
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Store.Path = filepath.Join(t.TempDir(), "engi.db")
	cfg.Credentials.ActiveKeyID = "k1"
	cfg.Credentials.Keys = map[string]config.Secret{"k1": config.Secret(base64.StdEncoding.EncodeToString(key))}
	// Cheap hashing and a cookie that plain-HTTP test servers can set.
	cfg.Auth.Argon2 = config.Argon2Config{Iterations: 1, MemoryKiB: 64, Threads: 1, SaltBytes: 16, KeyBytes: 32, MaxConcurrent: 2}
	cfg.Auth.Session.CookieSecure = config.CookieSecureNever
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.Server.SSEHeartbeat = 50 * time.Millisecond
	cfg.Queue.LeaseDuration, cfg.Queue.HeartbeatInterval = 400*time.Millisecond, 100*time.Millisecond
	cfg.Queue.PollInterval, cfg.Queue.RetryDelay = 20*time.Millisecond, 0
	cfg.Worker.ReclaimInterval, cfg.Worker.WaiterInterval = 100*time.Millisecond, 50*time.Millisecond
	cfg.Worker.ShutdownGrace = 2 * time.Second
	cfg.Server.ShutdownGrace = 2 * time.Second
	return cfg
}

func newServerFixture(t *testing.T, mutate ...func(*config.Config)) *serverFixture {
	t.Helper()
	cfg := testConfig(t)
	for _, m := range mutate {
		m(&cfg)
	}
	logs := &syncBuffer{}
	a, err := New(t.Context(), cfg, Options{
		Logger:    slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := a.NewServer(context.Background(), ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := &serverFixture{t: t, app: a, srv: srv, ts: httptest.NewServer(srv.API.Handler()), logs: logs}
	t.Cleanup(func() {
		f.ts.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return f
}

// startWorker runs the worker and wait scheduler.
func (f *serverFixture) startWorker() {
	f.t.Helper()
	if err := f.srv.Runtime.Start(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

// req describes one HTTP call.
type req struct {
	method, path string
	body         any    // marshaled as JSON unless it is a string or []byte
	raw          string // raw body, wins over body
	contentType  string
	headers      map[string]string
	cookies      []*http.Cookie
	bearer       string
}

type resp struct {
	*http.Response
	body []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, r.body)
	}
	return m
}

func (f *serverFixture) do(r req) resp {
	f.t.Helper()
	var rd io.Reader
	switch {
	case r.raw != "":
		rd = strings.NewReader(r.raw)
	case r.body != nil:
		b, err := json.Marshal(r.body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequestWithContext(f.t.Context(), r.method, f.ts.URL+r.path, rd)
	if err != nil {
		f.t.Fatal(err)
	}
	if rd != nil {
		ct := r.contentType
		if ct == "" {
			ct = "application/json"
		}
		httpReq.Header.Set("Content-Type", ct)
	}
	for k, v := range r.headers {
		httpReq.Header.Set(k, v)
	}
	for _, c := range r.cookies {
		httpReq.AddCookie(c)
	}
	if r.bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return resp{Response: res, body: b}
}

// session is a logged-in browser: cookie plus CSRF token.
type session struct {
	cookie *http.Cookie
	csrf   string
}

// call performs an authenticated, CSRF-protected call as the session.
func (f *serverFixture) as(s session, r req) resp {
	r.cookies = append(r.cookies, s.cookie)
	if r.headers == nil {
		r.headers = map[string]string{}
	}
	r.headers["X-CSRF-Token"] = s.csrf
	return f.do(r)
}

func (f *serverFixture) setupOwner() {
	f.t.Helper()
	res := f.do(req{method: "POST", path: "/api/v1/auth/setup", body: map[string]string{"email": testEmail, "name": "Owner", "password": testPassword}})
	if res.StatusCode != http.StatusCreated {
		f.t.Fatalf("setup = %d %s", res.StatusCode, res.body)
	}
}

func (f *serverFixture) login(email, password string) session {
	f.t.Helper()
	res := f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": email, "password": password}})
	if res.StatusCode != http.StatusOK {
		f.t.Fatalf("login = %d %s", res.StatusCode, res.body)
	}
	var s session
	for _, c := range res.Cookies() {
		if c.Name == "engi_session" {
			s.cookie = c
		}
	}
	if s.cookie == nil {
		f.t.Fatal("no session cookie")
	}
	s.csrf, _ = res.json(f.t)["csrfToken"].(string)
	return s
}

func (f *serverFixture) ownerSession() session {
	f.t.Helper()
	f.setupOwner()
	return f.login(testEmail, testPassword)
}

// apiKey creates a key with the scopes and returns its token.
func (f *serverFixture) apiKey(s session, scopes ...string) string {
	f.t.Helper()
	res := f.as(s, req{method: "POST", path: "/api/v1/api-keys", body: map[string]any{"name": "test", "scopes": scopes}})
	if res.StatusCode != http.StatusCreated {
		f.t.Fatalf("create key = %d %s", res.StatusCode, res.body)
	}
	tok, _ := res.json(f.t)["token"].(string)
	if tok == "" {
		f.t.Fatal("no token in the response")
	}
	return tok
}

const simpleWorkflow = `{
  "name": %q,
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "typeVersion": 1, "position": [0, 0]},
    {"name": "Compute", "type": "engi.code.javascript", "typeVersion": 1, "position": [200, 0],
     "parameters": {"mode": "runOnceForEachItem", "jsCode": "return { ...$json, doubled: ($json.n || 21) * 2 };"}}
  ],
  "connections": [{"from": {"node": "Start", "port": "main", "index": 0}, "to": {"node": "Compute", "port": "main", "index": 0}}]
}`
