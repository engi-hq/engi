package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServeEndToEnd boots the server as `engi serve` does (Run, real listener on
// a random port, worker and API roles), drives it over HTTP like a client would,
// and stops it with a canceled context, the way SIGTERM does.
func TestServeEndToEnd(t *testing.T) {
	cfg := testConfig(t)
	logs := &syncBuffer{}
	a, err := New(t.Context(), cfg, Options{LogOutput: logs, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := a.NewServer(t.Context(), ServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})

	var base string
	deadline := time.Now().Add(5 * time.Second)
	for base == "" && time.Now().Before(deadline) {
		if addr := srv.Addr(); addr != "" {
			base = "http://" + addr
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if base == "" {
		t.Fatal("server did not start listening")
	}

	call := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r, _ := http.NewRequestWithContext(t.Context(), method, base+path, rd)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}

	if code, _ := call("GET", "/healthz", "", nil); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	if code, _ := call("GET", "/api/v1/workflows", "", nil); code != 401 {
		t.Fatalf("unauthenticated list = %d", code)
	}

	// First run: create the owner, log in, mint an API key.
	if code, out := call("POST", "/api/v1/auth/setup", "", map[string]string{"email": testEmail, "name": "Owner", "password": testPassword}); code != 201 {
		t.Fatalf("setup = %d %v", code, out)
	}
	code, session := call("POST", "/api/v1/auth/login", "", map[string]string{"email": testEmail, "password": testPassword})
	if code != 200 || session["csrfToken"] == "" {
		t.Fatalf("login = %d %v", code, session)
	}
	// Mint the key with a cookie session (needs the CSRF header).
	var wfDoc map[string]any
	if err := json.Unmarshal([]byte(wfBody("e2e")), &wfDoc); err != nil {
		t.Fatal(err)
	}
	keyReq, _ := http.NewRequestWithContext(t.Context(), "POST", base+"/api/v1/api-keys", strings.NewReader(`{"name":"ci","scopes":["write"]}`))
	keyReq.Header.Set("Content-Type", "application/json")
	loginReq, _ := http.NewRequestWithContext(t.Context(), "POST", base+"/api/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, testEmail, testPassword)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRes, err := http.DefaultClient.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	var csrf string
	var lb map[string]any
	_ = json.NewDecoder(loginRes.Body).Decode(&lb)
	loginRes.Body.Close()
	csrf, _ = lb["csrfToken"].(string)
	for _, c := range loginRes.Cookies() {
		keyReq.AddCookie(c)
	}
	keyReq.Header.Set("X-CSRF-Token", csrf)
	keyRes, err := http.DefaultClient.Do(keyReq)
	if err != nil {
		t.Fatal(err)
	}
	var key map[string]any
	_ = json.NewDecoder(keyRes.Body).Decode(&key)
	keyRes.Body.Close()
	token, _ := key["token"].(string)
	if keyRes.StatusCode != 201 || token == "" {
		t.Fatalf("api key = %d %v", keyRes.StatusCode, key)
	}

	// Create and run a workflow with the key, then read the execution.
	code, wf := call("POST", "/api/v1/workflows", token, wfDoc)
	if code != 201 {
		t.Fatalf("create workflow = %d %v", code, wf)
	}
	wfID := wf["id"].(string)
	code, started := call("POST", "/api/v1/workflows/"+wfID+"/run", token, map[string]any{"items": []map[string]any{{"n": 4}}})
	if code != 202 {
		t.Fatalf("run = %d %v", code, started)
	}
	execID := started["executionId"].(string)
	var ex map[string]any
	for time.Now().Before(time.Now().Add(10*time.Second)) && (ex == nil || ex["status"] != "success") {
		if code, ex = call("GET", "/api/v1/executions/"+execID, token, nil); code != 200 {
			t.Fatalf("get execution = %d %v", code, ex)
		}
		if st := fmt.Sprint(ex["status"]); st == "error" || st == "crashed" || st == "canceled" {
			t.Fatalf("execution ended as %s: %v", st, ex)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(fmt.Sprint(ex["runData"]), "8") { // 4 doubled
		t.Fatalf("run data = %v", ex["runData"])
	}

	// SIGTERM equivalent: Run returns cleanly and the port is released.
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if _, err := http.Get(base + "/healthz"); err == nil { //nolint:noctx,bodyclose // test
		t.Fatal("server still answers after shutdown")
	}
	for _, leak := range []string{testPassword, token} {
		if strings.Contains(logs.String(), leak) {
			t.Fatalf("logs contain a secret: %q", leak)
		}
	}
}
