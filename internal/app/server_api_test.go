package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

func wfBody(name string) string { return fmt.Sprintf(simpleWorkflow, name) }

func wantStatus(t *testing.T, r resp, want int) {
	t.Helper()
	if r.StatusCode != want {
		t.Fatalf("status = %d, want %d\n%s", r.StatusCode, want, r.body)
	}
}

// wantProblem checks an RFC 9457 document.
func wantProblem(t *testing.T, r resp, status int) map[string]any {
	t.Helper()
	wantStatus(t, r, status)
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	p := r.json(t)
	if int(p["status"].(float64)) != status || p["title"] == "" || p["type"] == "" {
		t.Fatalf("incomplete problem: %v", p)
	}
	if p["requestId"] == "" || r.Header.Get("X-Request-ID") != p["requestId"] {
		t.Fatalf("request id mismatch: %v vs %q", p["requestId"], r.Header.Get("X-Request-ID"))
	}
	return p
}

func TestProbesAndRouting(t *testing.T) {
	f := newServerFixture(t)
	wantStatus(t, f.do(req{method: "GET", path: "/healthz"}), http.StatusOK)
	wantStatus(t, f.do(req{method: "GET", path: "/readyz"}), http.StatusOK)

	p := wantProblem(t, f.do(req{method: "GET", path: "/api/v1/nope"}), http.StatusNotFound)
	if !strings.HasSuffix(p["type"].(string), "/not-found") {
		t.Errorf("type = %v", p["type"])
	}
	r := f.do(req{method: "DELETE", path: "/api/v1/auth/login"})
	wantProblem(t, r, http.StatusMethodNotAllowed)
	if !strings.Contains(r.Header.Get("Allow"), "POST") {
		t.Errorf("Allow = %q", r.Header.Get("Allow"))
	}

	f.srv.API.Drain()
	wantStatus(t, f.do(req{method: "GET", path: "/readyz"}), http.StatusServiceUnavailable)
	wantStatus(t, f.do(req{method: "GET", path: "/healthz"}), http.StatusOK)
}

func TestRequestIDIsEchoedOrGenerated(t *testing.T) {
	f := newServerFixture(t)
	r := f.do(req{method: "GET", path: "/healthz", headers: map[string]string{"X-Request-ID": "abc-123"}})
	if got := r.Header.Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("echoed id = %q", got)
	}
	r = f.do(req{method: "GET", path: "/healthz", headers: map[string]string{"X-Request-ID": "bad id\twith spaces"}})
	if got := r.Header.Get("X-Request-ID"); !strings.HasPrefix(got, "rq_") {
		t.Errorf("generated id = %q", got)
	}
}

func TestAuthIsRequired(t *testing.T) {
	f := newServerFixture(t)
	for _, path := range []string{"/api/v1/workflows", "/api/v1/executions", "/api/v1/credentials", "/api/v1/node-types", "/api/v1/me", "/api/v1/users"} {
		r := f.do(req{method: "GET", path: path})
		wantProblem(t, r, http.StatusUnauthorized)
		if r.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", path)
		}
	}
	wantProblem(t, f.do(req{method: "GET", path: "/api/v1/me", bearer: "not-an-engi-key"}), http.StatusUnauthorized)
	wantProblem(t, f.do(req{method: "GET", path: "/api/v1/me", bearer: "engi_aaaaaaaaaaaa_bbbbbbbbbbbbbbbb"}), http.StatusUnauthorized)
}

func TestSetupLoginMeLogout(t *testing.T) {
	f := newServerFixture(t)
	if got := f.do(req{method: "GET", path: "/api/v1/auth/setup"}).json(t)["required"]; got != true {
		t.Fatalf("setup required = %v", got)
	}
	// Validation: weak password.
	p := wantProblem(t, f.do(req{method: "POST", path: "/api/v1/auth/setup", body: map[string]string{"email": testEmail, "name": "O", "password": "short"}}), http.StatusUnprocessableEntity)
	if errs, _ := p["errors"].([]any); len(errs) == 0 {
		t.Errorf("expected issues: %v", p)
	}
	f.setupOwner()
	wantProblem(t, f.do(req{method: "POST", path: "/api/v1/auth/setup", body: map[string]string{"email": "x@example.com", "name": "X", "password": testPassword}}), http.StatusConflict)
	if got := f.do(req{method: "GET", path: "/api/v1/auth/setup"}).json(t)["required"]; got != false {
		t.Fatalf("setup required after setup = %v", got)
	}

	wantProblem(t, f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": testEmail, "password": "wrong password"}}), http.StatusUnauthorized)

	res := f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": testEmail, "password": testPassword}})
	wantStatus(t, res, http.StatusOK)
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "engi_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("bad session cookie: %+v", cookie)
	}
	if strings.Contains(string(res.body), "passwordHash") || strings.Contains(string(res.body), testPassword) {
		t.Fatalf("login response leaks credentials: %s", res.body)
	}
	me := f.do(req{method: "GET", path: "/api/v1/me", cookies: []*http.Cookie{cookie}}).json(t)
	if me["authKind"] != "session" || me["csrfToken"] == "" || me["user"].(map[string]any)["role"] != "owner" {
		t.Fatalf("me = %v", me)
	}
	// The editor reads its server settings from /me at start.
	editor, _ := me["editor"].(map[string]any)
	if editor["chatBasePath"] != testConfig(t).Trigger.Chat.BasePath || editor["maxNoteTextBytes"] != float64(testConfig(t).Workflow.MaxNoteTextBytes) {
		t.Fatalf("me.editor = %v", me["editor"])
	}

	// Logout needs no CSRF token and clears the cookie.
	out := f.do(req{method: "POST", path: "/api/v1/auth/logout", cookies: []*http.Cookie{cookie}})
	wantStatus(t, out, http.StatusNoContent)
	wantProblem(t, f.do(req{method: "GET", path: "/api/v1/me", cookies: []*http.Cookie{cookie}}), http.StatusUnauthorized)
}

func TestLoginIsRateLimited(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) { c.Auth.LoginLimit.MaxFailures = 3 })
	f.setupOwner()
	for range 3 {
		wantProblem(t, f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": testEmail, "password": "wrong password"}}), http.StatusUnauthorized)
	}
	r := f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": testEmail, "password": testPassword}})
	wantProblem(t, r, http.StatusTooManyRequests)
	if r.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

func TestCSRFProtectsCookieSessionsOnly(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	create := req{method: "POST", path: "/api/v1/workflows", raw: wfBody("csrf"), cookies: []*http.Cookie{s.cookie}}
	wantProblem(t, f.do(create), http.StatusForbidden)
	create.headers = map[string]string{"X-CSRF-Token": "wrong"}
	wantProblem(t, f.do(create), http.StatusForbidden)
	create.headers = map[string]string{"X-CSRF-Token": s.csrf}
	wantStatus(t, f.do(create), http.StatusCreated)
	// Reads never need the token.
	wantStatus(t, f.do(req{method: "GET", path: "/api/v1/workflows", cookies: []*http.Cookie{s.cookie}}), http.StatusOK)
	// API keys are not ambient credentials: no CSRF header needed.
	key := f.apiKey(s, "write")
	wantStatus(t, f.do(req{method: "POST", path: "/api/v1/workflows", raw: wfBody("by key"), bearer: key}), http.StatusCreated)
}

func TestAPIKeyLifecycleAndScopes(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	readKey := f.apiKey(s, "read")
	writeKey := f.apiKey(s, "write")

	wantStatus(t, f.do(req{method: "GET", path: "/api/v1/workflows", bearer: readKey}), http.StatusOK)
	wantProblem(t, f.do(req{method: "POST", path: "/api/v1/workflows", raw: wfBody("nope"), bearer: readKey}), http.StatusForbidden)
	wantStatus(t, f.do(req{method: "POST", path: "/api/v1/workflows", raw: wfBody("ok"), bearer: writeKey}), http.StatusCreated)
	// A key cannot mint keys.
	wantProblem(t, f.do(req{method: "POST", path: "/api/v1/api-keys", body: map[string]any{"name": "k", "scopes": []string{"read"}}, bearer: writeKey}), http.StatusForbidden)

	// The list never shows tokens or hashes.
	list := f.as(s, req{method: "GET", path: "/api/v1/api-keys"})
	wantStatus(t, list, http.StatusOK)
	for _, secret := range []string{readKey, writeKey, "secretHash", "token"} {
		if strings.Contains(string(list.body), secret) {
			t.Fatalf("api key list contains %q: %s", secret, list.body)
		}
	}
	data := list.json(t)["data"].([]any)
	id := data[0].(map[string]any)["id"].(string)

	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/api-keys/" + id}), http.StatusNoContent)
	wantProblem(t, f.as(s, req{method: "DELETE", path: "/api/v1/api-keys/ak_notaulid"}), http.StatusNotFound)
	// One of the two keys is revoked now (newest first: the write key).
	if r := f.do(req{method: "GET", path: "/api/v1/workflows", bearer: writeKey}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d", r.StatusCode)
	}
	wantStatus(t, f.do(req{method: "GET", path: "/api/v1/workflows", bearer: readKey}), http.StatusOK)
}

func TestMembersAreLimited(t *testing.T) {
	f := newServerFixture(t)
	owner := f.ownerSession()
	wantStatus(t, f.as(owner, req{method: "POST", path: "/api/v1/users", body: map[string]string{"email": "m@example.com", "name": "Member", "password": "member password 1"}}), http.StatusCreated)
	wantProblem(t, f.as(owner, req{method: "POST", path: "/api/v1/users", body: map[string]string{"email": "M@example.com", "name": "Dup", "password": "member password 1"}}), http.StatusConflict)

	member := f.login("m@example.com", "member password 1")
	wantProblem(t, f.as(member, req{method: "GET", path: "/api/v1/users"}), http.StatusForbidden)
	wantProblem(t, f.as(member, req{method: "POST", path: "/api/v1/users", body: map[string]string{"email": "z@example.com", "name": "Z", "password": "member password 1"}}), http.StatusForbidden)
	wantStatus(t, f.as(member, req{method: "GET", path: "/api/v1/workflows"}), http.StatusOK)

	users := f.as(owner, req{method: "GET", path: "/api/v1/users"})
	wantStatus(t, users, http.StatusOK)
	if got := len(users.json(t)["data"].([]any)); got != 2 {
		t.Fatalf("users = %d", got)
	}
	if strings.Contains(string(users.body), "argon2") {
		t.Fatal("user list leaks password hashes")
	}
}

func TestRequestValidation(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) { c.Server.MaxBodyBytes = 2048 })
	s := f.ownerSession()
	post := func(r req) resp { r.method, r.path = "POST", "/api/v1/workflows"; return f.as(s, r) }

	wantProblem(t, post(req{raw: "{not json"}), http.StatusBadRequest)
	wantProblem(t, post(req{raw: `{"name":"x","nodes":[],"connections":[],"bogus":1}`}), http.StatusBadRequest)
	wantProblem(t, post(req{raw: wfBody("x"), contentType: "text/plain"}), http.StatusUnsupportedMediaType)
	wantProblem(t, post(req{raw: strings.Repeat(" ", 4096) + wfBody("big")}), http.StatusRequestEntityTooLarge)

	// Structurally valid JSON, invalid workflow: 422 with pointers.
	bad := `{"name":"bad","nodes":[{"name":"Start","type":"engi.nope","typeVersion":1,"position":[0,0]}],"connections":[]}`
	p := wantProblem(t, post(req{raw: bad}), http.StatusUnprocessableEntity)
	errs := p["errors"].([]any)
	if len(errs) == 0 || errs[0].(map[string]any)["pointer"] == "" {
		t.Fatalf("no issues: %v", p)
	}
	empty := `{"name":"","nodes":[],"connections":[]}`
	wantProblem(t, post(req{raw: empty}), http.StatusUnprocessableEntity)
}

func TestWorkflowLifecycle(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	created := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody("flow")})
	wantStatus(t, created, http.StatusCreated)
	doc := created.json(t)
	id := doc["id"].(string)
	if created.Header.Get("Location") != "/api/v1/workflows/"+id || created.Header.Get("ETag") != `"1"` {
		t.Fatalf("headers: %v", created.Header)
	}
	if doc["active"] == true {
		t.Fatal("new workflow must be inactive")
	}

	// Optimistic concurrency.
	wantProblem(t, f.as(s, req{method: "PUT", path: "/api/v1/workflows/" + id, raw: wfBody("stale"), headers: map[string]string{"If-Match": `"9"`}}), http.StatusConflict)
	wantProblem(t, f.as(s, req{method: "PUT", path: "/api/v1/workflows/" + id, raw: wfBody("x"), headers: map[string]string{"If-Match": "abc"}}), http.StatusBadRequest)
	upd := f.as(s, req{method: "PUT", path: "/api/v1/workflows/" + id, raw: wfBody("flow v2"), headers: map[string]string{"If-Match": `"1"`}})
	wantStatus(t, upd, http.StatusOK)
	if upd.Header.Get("ETag") != `"2"` || upd.json(t)["name"] != "flow v2" {
		t.Fatalf("update: %s %v", upd.body, upd.Header)
	}

	versions := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/versions"})
	wantStatus(t, versions, http.StatusOK)
	if got := len(versions.json(t)["data"].([]any)); got != 2 {
		t.Fatalf("versions = %d", got)
	}
	v1 := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/versions/1"})
	wantStatus(t, v1, http.StatusOK)
	if v1.json(t)["workflow"].(map[string]any)["name"] != "flow" {
		t.Fatalf("version 1 = %s", v1.body)
	}
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/versions/99"}), http.StatusNotFound)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/versions/zero"}), http.StatusBadRequest)
	rest := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/versions/1/restore"})
	wantStatus(t, rest, http.StatusOK)
	if rest.json(t)["name"] != "flow" || rest.Header.Get("ETag") != `"3"` {
		t.Fatalf("restore: %s", rest.body)
	}

	// Export, then import as a new workflow.
	exp := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/export"})
	wantStatus(t, exp, http.StatusOK)
	if !strings.Contains(exp.Header.Get("Content-Disposition"), id+".json") {
		t.Errorf("Content-Disposition = %q", exp.Header.Get("Content-Disposition"))
	}
	imp := f.as(s, req{method: "POST", path: "/api/v1/workflows/import", raw: string(exp.body)})
	wantStatus(t, imp, http.StatusCreated)
	if imp.json(t)["id"] == id {
		t.Fatal("import must not reuse the exported id")
	}

	// This workflow has no activatable trigger: the trigger service refuses it.
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/deactivate"}), http.StatusOK)

	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows/wf_nope"}), http.StatusNotFound)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows/wf_01J00000000000000000000000"}), http.StatusNotFound)
	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/workflows/" + id}), http.StatusNoContent)
	wantProblem(t, f.as(s, req{method: "DELETE", path: "/api/v1/workflows/" + id}), http.StatusNotFound)
}

func TestPaginationAndFilters(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	for i := range 5 {
		wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody(fmt.Sprintf("wf %d", i))}), http.StatusCreated)
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		path := "/api/v1/workflows?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		r := f.as(s, req{method: "GET", path: path})
		wantStatus(t, r, http.StatusOK)
		page := r.json(t)
		for _, it := range page["data"].([]any) {
			seen[it.(map[string]any)["id"].(string)] = true
		}
		pages++
		if page["hasMore"] != true {
			if _, has := page["nextCursor"]; has {
				t.Fatal("last page must not carry a cursor")
			}
			break
		}
		cursor = page["nextCursor"].(string)
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("saw %d workflows over %d pages", len(seen), pages)
	}
	// The page size is capped by configuration.
	big := f.as(s, req{method: "GET", path: "/api/v1/workflows?limit=100000"})
	wantStatus(t, big, http.StatusOK)

	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows?limit=0"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows?limit=abc"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows?cursor=%25%25"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows?colour=red"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/workflows?active=maybe"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/executions?status=bogus"}), http.StatusBadRequest)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/executions?from=yesterday"}), http.StatusBadRequest)

	byName := f.as(s, req{method: "GET", path: "/api/v1/workflows?q=wf+3"})
	wantStatus(t, byName, http.StatusOK)
	if got := len(byName.json(t)["data"].([]any)); got != 1 {
		t.Fatalf("name filter matched %d", got)
	}
}

func TestCredentialsAreRedacted(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	const secret = "s3cr3t-bearer-token-value"
	created := f.as(s, req{method: "POST", path: "/api/v1/credentials", body: map[string]any{
		"name": "my token", "type": "engi.http.bearer", "data": map[string]any{"token": secret}}})
	wantStatus(t, created, http.StatusCreated)
	id := created.json(t)["id"].(string)

	for _, r := range []resp{
		created,
		f.as(s, req{method: "GET", path: "/api/v1/credentials/" + id}),
		f.as(s, req{method: "GET", path: "/api/v1/credentials"}),
		f.as(s, req{method: "PUT", path: "/api/v1/credentials/" + id, body: map[string]any{"name": "renamed", "data": map[string]any{"token": "***"}}}),
	} {
		wantStatus(t, r, http.StatusOK+boolInt(r.Request.Method == "POST")*(http.StatusCreated-http.StatusOK))
		if strings.Contains(string(r.body), secret) {
			t.Fatalf("response leaks the secret: %s", r.body)
		}
	}
	got := f.as(s, req{method: "GET", path: "/api/v1/credentials/" + id}).json(t)
	if got["name"] != "renamed" || got["data"].(map[string]any)["token"] == secret {
		t.Fatalf("credential = %v", got)
	}
	list := f.as(s, req{method: "GET", path: "/api/v1/credentials"}).json(t)["data"].([]any)
	if _, hasData := list[0].(map[string]any)["data"]; hasData {
		t.Fatal("credential list must not include data")
	}

	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/credentials", body: map[string]any{"name": "x", "type": "engi.nope", "data": map[string]any{}}}), http.StatusUnprocessableEntity)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/credentials/" + id + "/test"}), http.StatusUnprocessableEntity) // bearer has no test
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/credentials/cr_01J00000000000000000000000/test"}), http.StatusNotFound)
	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/credentials/" + id}), http.StatusNoContent)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/credentials/" + id}), http.StatusNotFound)

	// Nothing the server logged contains the secret either.
	if strings.Contains(f.logs.String(), secret) {
		t.Fatal("secret in logs")
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestCatalogs(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	nodes := f.as(s, req{method: "GET", path: "/api/v1/node-types"})
	wantStatus(t, nodes, http.StatusOK)
	if !strings.Contains(string(nodes.body), `"engi.http.request"`) {
		t.Fatalf("node types: %.300s", nodes.body)
	}
	one := f.as(s, req{method: "GET", path: "/api/v1/node-types/engi.http.request"})
	wantStatus(t, one, http.StatusOK)
	if one.json(t)["type"] != "engi.http.request" {
		t.Fatalf("node type = %s", one.body)
	}
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/node-types/engi.nope"}), http.StatusNotFound)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/node-types/engi.http.request?version=99"}), http.StatusNotFound)

	ct := f.as(s, req{method: "GET", path: "/api/v1/credential-types/engi.http.bearer"})
	wantStatus(t, ct, http.StatusOK)
	fields := ct.json(t)["fields"].([]any)
	if len(fields) == 0 || fields[0].(map[string]any)["secret"] != true {
		t.Fatalf("bearer fields = %v", fields)
	}
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/credential-types/engi.nope"}), http.StatusNotFound)
	wantStatus(t, f.as(s, req{method: "GET", path: "/api/v1/credential-types"}), http.StatusOK)
}

func TestOAuth2CallbackRejectsBadState(t *testing.T) {
	f := newServerFixture(t)
	wantProblem(t, f.do(req{method: "GET", path: "/oauth2/callback?state=garbage&code=abc"}), http.StatusBadRequest)
	wantProblem(t, f.do(req{method: "GET", path: "/oauth2/callback?error=access_denied"}), http.StatusBadRequest)
	wantProblem(t, f.do(req{method: "GET", path: "/oauth2/callback?evil=1"}), http.StatusBadRequest)
}

func TestRunWorkflowAndReadExecution(t *testing.T) {
	f := newServerFixture(t)
	f.startWorker()
	s := f.ownerSession()
	created := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody("runner")})
	id := created.json(t)["id"].(string)

	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/run", body: map[string]any{"startNode": "Nope"}}), http.StatusUnprocessableEntity)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/workflows/wf_01J00000000000000000000000/run"}), http.StatusNotFound)

	run := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/run", body: map[string]any{"items": []map[string]any{{"n": 5}}}})
	wantStatus(t, run, http.StatusAccepted)
	execID := run.json(t)["executionId"].(string)
	if run.Header.Get("Location") != "/api/v1/executions/"+execID {
		t.Fatalf("Location = %q", run.Header.Get("Location"))
	}

	ex := waitForExecution(t, f, s, execID, "success")
	runData := ex["runData"].(map[string]any)
	if _, ok := runData["Compute"]; !ok || ex["mode"] != "manual" || ex["workflowId"] != id {
		t.Fatalf("execution = %v", ex)
	}
	if !strings.Contains(fmt.Sprint(runData["Compute"]), "10") { // 5 * 2
		t.Fatalf("Compute output: %v", runData["Compute"])
	}

	list := f.as(s, req{method: "GET", path: "/api/v1/executions?status=success&workflowId=" + id})
	wantStatus(t, list, http.StatusOK)
	items := list.json(t)["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("executions = %d", len(items))
	}
	if _, has := items[0].(map[string]any)["runData"]; has {
		t.Fatal("list items must not carry run data")
	}
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/executions/" + execID + "/cancel"}), http.StatusConflict)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/executions/" + execID + "/retry"}), http.StatusConflict)
	wantStatus(t, f.as(s, req{method: "DELETE", path: "/api/v1/executions/" + execID}), http.StatusNoContent)
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/executions/" + execID}), http.StatusNotFound)
}

func waitForExecution(t *testing.T, f *serverFixture, s session, id, status string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r := f.as(s, req{method: "GET", path: "/api/v1/executions/" + id})
		wantStatus(t, r, http.StatusOK)
		ex := r.json(t)
		if ex["status"] == status {
			return ex
		}
		if st, _ := ex["status"].(string); domain.ExecutionStatus(st).Terminal() {
			t.Fatalf("execution ended as %v: %s", st, r.body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach %s", id, status)
	return nil
}

func TestCancelAndRetryQueuedExecutions(t *testing.T) {
	f := newServerFixture(t) // no worker: executions stay queued
	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody("q")}).json(t)["id"].(string)
	execID := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/run"}).json(t)["executionId"].(string)

	wantProblem(t, f.as(s, req{method: "DELETE", path: "/api/v1/executions/" + execID}), http.StatusConflict) // not finished
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/executions/" + execID + "/retry"}), http.StatusConflict)
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/executions/" + execID + "/cancel"}), http.StatusAccepted)
	ex := f.as(s, req{method: "GET", path: "/api/v1/executions/" + execID}).json(t)
	if ex["status"] != "canceled" {
		t.Fatalf("status = %v", ex["status"])
	}
}

// sseFrame is one parsed server-sent event.
type sseFrame struct{ id, event, data string }

func readFrames(t *testing.T, sc *bufio.Scanner, n int) []sseFrame {
	t.Helper()
	var frames []sseFrame
	var cur sseFrame
	for len(frames) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.event != "" {
				frames = append(frames, cur)
			}
			cur = sseFrame{}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			cur.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		}
	}
	return frames
}

func TestStreamDeliversExecutionEvents(t *testing.T) {
	f := newServerFixture(t) // queued execution; the test plays the worker by publishing events
	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody("s")}).json(t)["id"].(string)
	execID := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/run"}).json(t)["executionId"].(string)

	httpReq, _ := http.NewRequestWithContext(t.Context(), "GET", f.ts.URL+"/api/v1/executions/"+execID+"/stream", http.NoBody)
	httpReq.AddCookie(s.cookie)
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %v", res.StatusCode, res.Header)
	}
	sc := bufio.NewScanner(res.Body)

	// The handler subscribes before it writes the ": stream open" comment, so
	// once that line has been read, publishing cannot be missed.
	bus := f.srv.Runtime.Bus
	now := time.Now()
	events := []port.Event{
		{Kind: port.EventExecutionStarted, ExecutionID: domain.ExecutionID(execID), At: now},
		{Kind: port.EventNodeStarted, ExecutionID: domain.ExecutionID(execID), Node: "Start", At: now},
		{Kind: port.EventNodeFinished, ExecutionID: domain.ExecutionID(execID), Node: "Start", At: now,
			Run: &domain.NodeRun{Status: domain.NodeRunSuccess, Attempts: 1, Output: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"secret": "item-payload"}}}}}}},
		{Kind: port.EventCancelRequested, ExecutionID: domain.ExecutionID(execID), At: now}, // not forwarded
		{Kind: port.EventExecutionFinished, ExecutionID: domain.ExecutionID(execID), Status: domain.StatusSuccess, At: now},
	}
	if !sc.Scan() || sc.Text() != ": stream open" {
		t.Fatalf("first line = %q", sc.Text())
	}
	for _, e := range events {
		if err := bus.Publish(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	frames := readFrames(t, sc, 4)
	want := []string{"execution.started", "node.started", "node.finished", "execution.finished"}
	if len(frames) != len(want) {
		t.Fatalf("frames = %+v", frames)
	}
	for i, w := range want {
		if frames[i].event != w || frames[i].id != fmt.Sprint(i+1) {
			t.Errorf("frame %d = %+v, want event %s", i, frames[i], w)
		}
	}
	if !strings.Contains(frames[2].data, `"node":"Start"`) || !strings.Contains(frames[2].data, `"outputItems":[1]`) {
		t.Errorf("node.finished data = %s", frames[2].data)
	}
	if strings.Contains(frames[2].data, "item-payload") {
		t.Error("item payloads must not travel on the stream")
	}
	if sc.Scan() { // the stream closes after the final event (heartbeat comments aside)
		for sc.Scan() {
			if !strings.HasPrefix(sc.Text(), ":") && sc.Text() != "" {
				t.Fatalf("data after the final event: %q", sc.Text())
			}
		}
	}
}

func TestStreamOfFinishedExecutionAnswersAtOnce(t *testing.T) {
	f := newServerFixture(t)
	f.startWorker()
	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: wfBody("done")}).json(t)["id"].(string)
	execID := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/run"}).json(t)["executionId"].(string)
	waitForExecution(t, f, s, execID, "success")

	httpReq, _ := http.NewRequestWithContext(t.Context(), "GET", f.ts.URL+"/api/v1/executions/"+execID+"/stream", http.NoBody)
	httpReq.AddCookie(s.cookie)
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	frames := readFrames(t, bufio.NewScanner(res.Body), 1)
	if len(frames) != 1 || frames[0].event != "execution.finished" || !strings.Contains(frames[0].data, `"status":"success"`) {
		t.Fatalf("frames = %+v", frames)
	}
	wantProblem(t, f.as(s, req{method: "GET", path: "/api/v1/executions/ex_01J00000000000000000000000/stream"}), http.StatusNotFound)
	wantProblem(t, f.do(req{method: "GET", path: "/api/v1/executions/" + execID + "/stream"}), http.StatusUnauthorized)
}

func TestCORSAllowList(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) {
		c.Server.CORS.AllowedOrigins = []string{"https://app.example.com"}
		c.Server.CORS.AllowCredentials = true
	})
	pre := f.do(req{method: "OPTIONS", path: "/api/v1/workflows", headers: map[string]string{
		"Origin": "https://app.example.com", "Access-Control-Request-Method": "POST"}})
	wantStatus(t, pre, http.StatusNoContent)
	if pre.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		pre.Header.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "X-CSRF-Token") {
		t.Fatalf("preflight headers: %v", pre.Header)
	}
	other := f.do(req{method: "GET", path: "/healthz", headers: map[string]string{"Origin": "https://evil.example.com"}})
	if other.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin allowed: %v", other.Header)
	}
	same := f.do(req{method: "GET", path: "/healthz", headers: map[string]string{"Origin": "https://app.example.com"}})
	if same.Header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("allowed origin missing: %v", same.Header)
	}
}

func TestAccessLogOmitsSecrets(t *testing.T) {
	f := newServerFixture(t)
	f.setupOwner()
	f.do(req{method: "POST", path: "/api/v1/auth/login", body: map[string]string{"email": testEmail, "password": testPassword}})
	f.do(req{method: "GET", path: "/oauth2/callback?state=SUPERSECRETSTATE&code=SUPERSECRETCODE"})
	logs := f.logs.String()
	if !strings.Contains(logs, "http request") {
		t.Fatal("no access log line")
	}
	for _, leak := range []string{testPassword, "SUPERSECRETSTATE", "SUPERSECRETCODE"} {
		if strings.Contains(logs, leak) {
			t.Fatalf("logs contain %q", leak)
		}
	}
}

func TestMountsAndRoles(t *testing.T) {
	cfg := testConfig(t)
	cfg.Server.Roles = []string{config.RoleWebhook}
	a, err := New(t.Context(), cfg, Options{LookupEnv: func(string) (string, bool) { return "", false }, LogOutput: &syncBuffer{}})
	if err != nil {
		t.Fatal(err)
	}
	hook := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hook")) })
	srv, err := a.NewServer(t.Context(), ServerOptions{Mounts: map[string]http.Handler{"/hook/": hook}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	get := func(path string) (int, string) {
		res, err := http.Get("http://" + srv.Addr() + path) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b := make([]byte, 64)
		n, _ := res.Body.Read(b)
		return res.StatusCode, string(b[:n])
	}
	if code, body := get("/hook/abc"); code != 200 || body != "hook" {
		t.Fatalf("mount: %d %q", code, body)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("healthz without the api role: %d", code)
	}
	if code, _ := get("/api/v1/workflows"); code != 404 {
		t.Fatalf("api routes must not exist without the api role: %d", code)
	}
}

func TestServerRefusesToStartWithoutKeyring(t *testing.T) {
	cfg := testConfig(t)
	cfg.Credentials = config.Default().Credentials
	a, err := New(t.Context(), cfg, Options{LookupEnv: func(string) (string, bool) { return "", false }, LogOutput: &syncBuffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.NewServer(t.Context(), ServerOptions{}); err == nil || !strings.Contains(err.Error(), "ENGI_ENCRYPTION_KEYS") {
		t.Fatalf("err = %v", err)
	}
}

func TestAutoMigrateOffRequiresMigratedSchema(t *testing.T) {
	cfg := testConfig(t)
	cfg.Server.AutoMigrate = false
	a, err := New(t.Context(), cfg, Options{LookupEnv: func(string) (string, bool) { return "", false }, LogOutput: &syncBuffer{}})
	if err == nil {
		_ = a.CloseCredentials()
		t.Fatal("expected an out-of-date schema error")
	}
	if !errors.Is(err, ErrSchemaOutdated) {
		t.Fatalf("err = %v", err)
	}
	if err := MigrateUp(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	a, err = New(t.Context(), cfg, Options{LookupEnv: func(string) (string, bool) { return "", false }, LogOutput: &syncBuffer{}})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.CloseCredentials()
}

// TestAnAPIOnlyProcessActivatesForTheWebhookRole is the cluster split: the
// api pod activates, a separate webhook pod on the same database serves the
// route. Arming a test webhook still needs the webhook role in the process.
func TestAnAPIOnlyProcessActivatesForTheWebhookRole(t *testing.T) {
	api := newServerFixture(t, func(c *config.Config) { c.Server.Roles = []string{config.RoleAPI} })
	s := api.ownerSession()
	id := api.as(s, req{method: "POST", path: "/api/v1/workflows", raw: webhookWorkflow}).json(t)["id"].(string)
	wantStatus(t, api.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/activate"}), http.StatusOK)
	if active := api.as(s, req{method: "GET", path: "/api/v1/workflows/" + id}).json(t)["active"]; active != true {
		t.Fatalf("active = %v after activating from the api role", active)
	}
	wantProblem(t, api.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/test-webhook"}), http.StatusNotImplemented)

	hooks := newServerFixture(t, func(c *config.Config) {
		c.Server.Roles = []string{config.RoleWebhook}
		c.Store.Path = api.app.Config.Store.Path
	})
	route, err := hooks.srv.Runtime.Triggers.ResolveLive(t.Context(), http.MethodPost, "ping")
	if err != nil {
		t.Fatalf("the webhook role does not serve the route activated by the api role: %v", err)
	}
	if string(route.WorkflowID) != id {
		t.Fatalf("route workflow = %s, want %s", route.WorkflowID, id)
	}
}

const webhookWorkflow = `{
  "name": "hook",
  "nodes": [
    {"name": "Hook", "type": "engi.trigger.webhook", "typeVersion": 1, "position": [0, 0],
     "parameters": {"httpMethod": "post", "path": "ping", "responseMode": "lastNode"}},
    {"name": "Tag", "type": "engi.transform.set", "typeVersion": 1, "position": [200, 0],
     "parameters": {"fields": [{"name": "tier", "type": "string", "value": "gold"}]}}
  ],
  "connections": [{"from": {"node": "Hook", "port": "main", "index": 0}, "to": {"node": "Tag", "port": "main", "index": 0}}]
}`

// TestActivatedWebhookAnswersThroughTheServer drives the whole path: create a
// webhook workflow through the API, activate it, call the public hook URL.
func TestActivatedWebhookAnswersThroughTheServer(t *testing.T) {
	cfg := testConfig(t)
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
	base := "http://" + srv.Addr()
	f := &serverFixture{t: t, app: a, srv: srv, ts: &httptest.Server{URL: base}, logs: &syncBuffer{}}

	s := f.ownerSession()
	id := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: webhookWorkflow}).json(t)["id"].(string)
	act := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/activate"})
	wantStatus(t, act, http.StatusOK)
	if act.json(t)["active"] != true {
		t.Fatalf("not active: %s", act.body)
	}

	// The hook is public: no cookie, no CSRF header.
	res := f.do(req{method: "POST", path: "/hook/ping", body: map[string]any{"a": 1}})
	wantStatus(t, res, http.StatusOK)
	if !strings.Contains(string(res.body), `"tier":"gold"`) {
		t.Fatalf("hook answered %s", res.body)
	}

	// Test webhook URL.
	arm := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/test-webhook", body: map[string]string{"node": "Hook"}})
	wantStatus(t, arm, http.StatusOK)
	if !strings.HasPrefix(arm.json(t)["urlPath"].(string), "/hook-test/") {
		t.Fatalf("test webhook = %s", arm.body)
	}
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/deactivate"}), http.StatusOK)
	wantStatus(t, f.do(req{method: "POST", path: "/hook/ping", body: map[string]any{}}), http.StatusNotFound)
}

// notedBody is a workflow document with two sticky notes.
func notedBody(name string) string {
	return strings.Replace(wfBody(name), `"nodes"`, `"notes": [
	  {"id": "nt_a", "text": "# Plan\n\n- one", "position": [10, 20], "size": {"width": 300, "height": 120}, "color": "blue"},
	  {"id": "nt_b", "text": "second", "position": [0, 0], "size": {"width": 240, "height": 160}}],
	"nodes"`, 1)
}

func notesOf(t *testing.T, doc map[string]any) []any {
	t.Helper()
	notes, _ := doc["notes"].([]any)
	return notes
}

func TestWorkflowNotesPersistThroughVersionsAndExport(t *testing.T) {
	f := newServerFixture(t)
	s := f.ownerSession()
	created := f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: notedBody("noted")})
	wantStatus(t, created, http.StatusCreated)
	id := created.json(t)["id"].(string)
	if got := notesOf(t, created.json(t)); len(got) != 2 || got[0].(map[string]any)["color"] != "blue" {
		t.Fatalf("create response notes = %v", got)
	}

	got := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id})
	wantStatus(t, got, http.StatusOK)
	first := notesOf(t, got.json(t))
	if len(first) != 2 || first[0].(map[string]any)["text"] != "# Plan\n\n- one" {
		t.Fatalf("get notes = %v", first)
	}

	// Version 2 drops the notes; version 1 must still hold them, and restoring brings them back.
	wantStatus(t, f.as(s, req{method: "PUT", path: "/api/v1/workflows/" + id, raw: wfBody("plain")}), http.StatusOK)
	v1 := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/versions/1"})
	wantStatus(t, v1, http.StatusOK)
	if n := notesOf(t, v1.json(t)["workflow"].(map[string]any)); len(n) != 2 {
		t.Fatalf("version 1 lost its notes: %s", v1.body)
	}
	rest := f.as(s, req{method: "POST", path: "/api/v1/workflows/" + id + "/versions/1/restore"})
	wantStatus(t, rest, http.StatusOK)
	if len(notesOf(t, rest.json(t))) != 2 {
		t.Fatalf("restore dropped notes: %s", rest.body)
	}

	exp := f.as(s, req{method: "GET", path: "/api/v1/workflows/" + id + "/export"})
	wantStatus(t, exp, http.StatusOK)
	imp := f.as(s, req{method: "POST", path: "/api/v1/workflows/import", raw: string(exp.body)})
	wantStatus(t, imp, http.StatusCreated)
	if len(notesOf(t, imp.json(t))) != 2 {
		t.Fatalf("import dropped notes: %s", imp.body)
	}
}

func TestWorkflowNoteLimitsAndValidation(t *testing.T) {
	f := newServerFixture(t, func(c *config.Config) { c.Workflow.MaxNotes = 2; c.Workflow.MaxNoteTextBytes = 16 })
	s := f.ownerSession()
	wantStatus(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: strings.Replace(notedBody("fits"), `"text": "second"`, `"text": "short"`, 1)}), http.StatusCreated)
	many := strings.Replace(notedBody("many"), `"color": "blue"}`, `"color": "blue"}, {"id": "nt_c", "position": [0, 0], "size": {"width": 1, "height": 1}}`, 1)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: many}), http.StatusUnprocessableEntity)

	long := strings.Replace(notedBody("long"), `"text": "second"`, `"text": "this text is much too long"`, 1)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: long}), http.StatusUnprocessableEntity)

	bad := strings.Replace(notedBody("bad"), `"color": "blue"`, `"color": "plaid"`, 1)
	wantProblem(t, f.as(s, req{method: "POST", path: "/api/v1/workflows", raw: bad}), http.StatusUnprocessableEntity)
}
