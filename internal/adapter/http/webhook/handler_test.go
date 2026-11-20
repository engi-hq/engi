package webhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/http/webhook"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/internal/service/waiter"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type router struct {
	live, test map[string]trigger.Route // "METHOD path"
}

func (r router) find(m map[string]trigger.Route, method, path string) (trigger.Route, error) {
	if rt, ok := m[method+" "+path]; ok {
		return rt, nil
	}
	return trigger.Route{}, domain.ErrNotFound
}
func (r router) ResolveLive(_ context.Context, m, p string) (trigger.Route, error) {
	return r.find(r.live, m, p)
}

func (r router) ResolveTest(_ context.Context, m, p string) (trigger.Route, error) {
	rt, err := r.find(r.test, m, p)
	delete(r.test, m+" "+p) // one shot, like the real service
	return rt, err
}

type execs struct {
	mu      sync.Mutex
	started []execution.StartRequest
	start   func(execution.StartRequest) (domain.ExecutionID, error)
	runSync func(context.Context, execution.StartRequest) (domain.Execution, error)
	await   func(domain.ExecutionID) (domain.Execution, error)
}

func (e *execs) Start(_ context.Context, r execution.StartRequest) (domain.ExecutionID, error) {
	e.mu.Lock()
	e.started = append(e.started, r)
	e.mu.Unlock()
	if e.start != nil {
		return e.start(r)
	}
	return "exec_1", nil
}

func (e *execs) RunSync(ctx context.Context, r execution.StartRequest) (domain.Execution, error) {
	e.mu.Lock()
	e.started = append(e.started, r)
	e.mu.Unlock()
	return e.runSync(ctx, r)
}

func (e *execs) Await(_ context.Context, id domain.ExecutionID) (domain.Execution, error) {
	return e.await(id)
}

type resumer struct {
	token string
	items []domain.Item
	err   error
}

func (r *resumer) ResumeByToken(_ context.Context, token string, items []domain.Item) error {
	r.token, r.items = token, items
	return r.err
}

type creds map[domain.CredentialID]map[string]any

func (c creds) Resolve(_ context.Context, id domain.CredentialID) (port.CredentialData, error) {
	d, ok := c[id]
	if !ok {
		return port.CredentialData{}, domain.ErrNotFound
	}
	return port.CredentialData{Data: d}, nil
}

type env struct {
	h       *webhook.Handler
	router  router
	execs   *execs
	resumer *resumer
	bus     *servicetest.Bus
	srv     *httptest.Server
}

func newEnv(t *testing.T, mutate ...func(*config.WebhookConfig)) *env {
	t.Helper()
	cfg := config.Default().Trigger.Webhook
	cfg.ResponseTimeout = 300 * time.Millisecond
	cfg.MaxBodyBytes = 64
	for _, m := range mutate {
		m(&cfg)
	}
	e := &env{
		router: router{live: map[string]trigger.Route{}, test: map[string]trigger.Route{}}, execs: &execs{}, resumer: &resumer{}, bus: servicetest.NewBus(),
	}
	h, err := webhook.New(cfg, webhook.Deps{
		Router: e.router, Executions: e.execs, Resumer: e.resumer, Bus: e.bus,
		Credentials: creds{
			"basic":  {"user": "ann", "password": "pw"},
			"header": {"name": "X-Token", "value": "s3cret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	mux := http.NewServeMux()
	for _, p := range h.Patterns() {
		mux.Handle(p, h)
	}
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func route(spec nodesdk.WebhookSpec) trigger.Route {
	if spec.Method == "" {
		spec.Method = http.MethodPost
	}
	return trigger.Route{WorkflowID: "wf_1", Node: "In", Spec: spec, Params: map[string]string{}, Mode: domain.ModeWebhook}
}

func (e *env) do(t *testing.T, method, path, body string, hdr ...string) (resp *http.Response, text string) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, sb.String()
}

func (e *env) lastRequest(t *testing.T) execution.StartRequest {
	t.Helper()
	e.execs.mu.Lock()
	defer e.execs.mu.Unlock()
	if len(e.execs.started) == 0 {
		t.Fatal("no execution was started")
	}
	return e.execs.started[len(e.execs.started)-1]
}

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %q", s)
	}
	return m
}

func TestRoutingAndTheTriggerItem(t *testing.T) {
	e := newEnv(t)
	rt := route(nodesdk.WebhookSpec{})
	rt.Params = map[string]string{"id": "42"}
	e.router.live["POST orders/42"] = rt

	resp, body := e.do(t, "POST", "/hook/orders/42?a=1&b=x&b=y", `{"n":1}`,
		"Content-Type", "application/json", "X-Custom", "v", "Authorization", "Bearer topsecret", "Cookie", "sid=1")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if got := jsonOf(t, body); got["executionId"] != "exec_1" {
		t.Fatalf("body = %s", body)
	}
	req := e.lastRequest(t)
	if req.WorkflowID != "wf_1" || req.StartNode != "In" || req.Mode != domain.ModeWebhook || len(req.Items) != 1 {
		t.Fatalf("request = %+v", req)
	}
	j := req.Items[0].JSON
	hdrs := j["headers"].(map[string]any)
	if hdrs["x-custom"] != "v" || hdrs["authorization"] != nil || hdrs["cookie"] != nil {
		t.Fatalf("headers = %v (credentials must not be kept)", hdrs)
	}
	if j["params"].(map[string]any)["id"] != "42" {
		t.Fatalf("params = %v", j["params"])
	}
	q := j["query"].(map[string]any)
	if q["a"] != "1" || fmt.Sprint(q["b"]) != "[x y]" {
		t.Fatalf("query = %v", q)
	}
	if j["body"].(map[string]any)["n"] != 1.0 {
		t.Fatalf("body = %v", j["body"])
	}

	for _, tc := range []struct{ method, path string }{{"GET", "/hook/orders/42"}, {"POST", "/hook/orders"}, {"POST", "/other/x"}} {
		if resp, _ := e.do(t, tc.method, tc.path, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestBodyParsing(t *testing.T) {
	e := newEnv(t, func(c *config.WebhookConfig) { c.MaxBodyBytes = 1 << 10 })
	e.router.live["POST plain"] = route(nodesdk.WebhookSpec{})
	e.router.live["POST raw"] = route(nodesdk.WebhookSpec{RawBody: true})
	body := func() any { return e.lastRequest(t).Items[0].JSON["body"] }

	e.do(t, "POST", "/hook/plain", "a=1&a=2&b=x", "Content-Type", "application/x-www-form-urlencoded")
	if m := body().(map[string]any); m["b"] != "x" || fmt.Sprint(m["a"]) != "[1 2]" {
		t.Fatalf("form = %v", m)
	}
	e.do(t, "POST", "/hook/plain", "", "Content-Type", "application/json")
	if m := body().(map[string]any); len(m) != 0 {
		t.Fatalf("empty = %v", m)
	}
	e.do(t, "POST", "/hook/plain", "hello", "Content-Type", "text/plain")
	if body() != "hello" {
		t.Fatalf("text = %v", body())
	}
	e.do(t, "POST", "/hook/plain", `[1,2]`, "Content-Type", "application/json")
	if fmt.Sprint(body()) != "[1 2]" {
		t.Fatalf("array = %v", body())
	}
	e.do(t, "POST", "/hook/raw", `{"a":1}`, "Content-Type", "application/json")
	if body() != `{"a":1}` {
		t.Fatalf("raw = %v", body())
	}
	mp := "--B\r\nContent-Disposition: form-data; name=\"f\"\r\n\r\nv\r\n--B--\r\n"
	e.do(t, "POST", "/hook/plain", mp, "Content-Type", "multipart/form-data; boundary=B")
	if m := body().(map[string]any); m["f"] != "v" {
		t.Fatalf("multipart = %v", m)
	}
	file := "--B\r\nContent-Disposition: form-data; name=\"f\"; filename=\"a.txt\"\r\n\r\nv\r\n--B--\r\n"
	if resp, _ := e.do(t, "POST", "/hook/plain", file, "Content-Type", "multipart/form-data; boundary=B"); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("file upload = %d", resp.StatusCode)
	}
	n := len(e.execs.started)
	if resp, _ := e.do(t, "POST", "/hook/plain", `{bad`, "Content-Type", "application/json"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid json = %d", resp.StatusCode)
	}
	if len(e.execs.started) != n {
		t.Fatal("a rejected body must not start an execution")
	}
}

func TestBodySizeLimit(t *testing.T) {
	e := newEnv(t) // 64 bytes
	e.router.live["POST big"] = route(nodesdk.WebhookSpec{})
	resp, _ := e.do(t, "POST", "/hook/big", strings.Repeat("x", 65), "Content-Type", "text/plain")
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(e.execs.started) != 0 {
		t.Fatal("started")
	}
	// chunked bodies have no Content-Length: the reader enforces the limit
	req, _ := http.NewRequest("POST", e.srv.URL+"/hook/big", io_(strings.Repeat("x", 100)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "text/plain")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked status = %d", r2.StatusCode)
	}
}

func io_(s string) *strings.Reader { return strings.NewReader(s) }

func TestImmediateResponseOptions(t *testing.T) {
	e := newEnv(t)
	e.router.live["POST a"] = route(nodesdk.WebhookSpec{ResponseCode: 201, ResponseBody: `{"ok":true}`})
	e.router.live["POST b"] = route(nodesdk.WebhookSpec{ResponseBody: "plain text"})
	resp, body := e.do(t, "POST", "/hook/a", "")
	if resp.StatusCode != 201 || body != `{"ok":true}` || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("a: %d %q %s", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	resp, body = e.do(t, "POST", "/hook/b", "")
	if resp.StatusCode != 202 || body != "plain text" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("b: %d %q", resp.StatusCode, body)
	}
}

func TestLastNodeResponse(t *testing.T) {
	e := newEnv(t)
	done := func(status domain.ExecutionStatus) domain.Execution {
		return domain.Execution{
			ID: "exec_1", Status: status,
			RunData: map[string][]domain.NodeRun{"Set": {{Status: domain.NodeRunSuccess, Output: domain.PortData{domain.PortMain: {{
				{JSON: map[string]any{"v": 1.0}}, {JSON: map[string]any{"v": 2.0}}}}}}}},
		}
	}
	e.execs.runSync = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return done(domain.StatusSuccess), nil
	}
	e.router.live["POST first"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseLastNode, ResponseData: "firstEntryJson"})
	e.router.live["POST all"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseLastNode, ResponseData: "allEntries", ResponseCode: 201})
	e.router.live["POST none"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseLastNode, ResponseData: "noData"})

	if resp, body := e.do(t, "POST", "/hook/first", ""); resp.StatusCode != 200 || jsonOf(t, body)["v"] != 1.0 {
		t.Fatalf("first: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do(t, "POST", "/hook/all", ""); resp.StatusCode != 201 || body != `[{"v":1},{"v":2}]` {
		t.Fatalf("all: %d %s", resp.StatusCode, body)
	}
	if resp, body := e.do(t, "POST", "/hook/none", ""); resp.StatusCode != 200 || body != "" {
		t.Fatalf("none: %d %q", resp.StatusCode, body)
	}

	e.execs.runSync = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return done(domain.StatusError), nil
	}
	resp, body := e.do(t, "POST", "/hook/first", "")
	if resp.StatusCode != 500 || jsonOf(t, body)["executionId"] != "exec_1" || strings.Contains(body, "v") && strings.Contains(body, `"v"`) {
		t.Fatalf("failed run: %d %s", resp.StatusCode, body)
	}
}

func TestLastNodeTimeouts(t *testing.T) {
	e := newEnv(t)
	e.router.live["POST slow"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseLastNode})
	e.execs.runSync = func(ctx context.Context, _ execution.StartRequest) (domain.Execution, error) {
		<-ctx.Done()
		return domain.Execution{}, fmt.Errorf("waiting: %w", ctx.Err())
	}
	if resp, _ := e.do(t, "POST", "/hook/slow", ""); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	e.execs.runSync = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return domain.Execution{}, fmt.Errorf("x: %w", execution.ErrSyncTimeout)
	}
	if resp, _ := e.do(t, "POST", "/hook/slow", ""); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("sync timeout status = %d", resp.StatusCode)
	}
}

func TestRespondToWebhookNodeAnswers(t *testing.T) {
	e := newEnv(t)
	e.router.live["POST r"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseNode})
	e.execs.start = func(execution.StartRequest) (domain.ExecutionID, error) {
		go func() {
			// another execution's response must be ignored
			_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventNodeFinished, ExecutionID: "exec_other", Run: &domain.NodeRun{
				Response: &domain.WebhookResponse{Status: 500, Body: []byte("wrong")}}})
			_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventNodeFinished, ExecutionID: "exec_1", Run: &domain.NodeRun{
				Response: &domain.WebhookResponse{Status: 418, Headers: map[string][]string{"X-A": {"1", "2"}, "Content-Length": {"999"}}, Body: []byte("tea")}}})
			// the workflow carries on after the reply
			_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventExecutionFinished, ExecutionID: "exec_1", Status: domain.StatusSuccess})
		}()
		return "exec_1", nil
	}
	resp, body := e.do(t, "POST", "/hook/r", "")
	if resp.StatusCode != 418 || body != "tea" || strings.Join(resp.Header.Values("X-A"), ",") != "1,2" {
		t.Fatalf("%d %q %v", resp.StatusCode, body, resp.Header)
	}
}

func TestRespondNodeFailureModes(t *testing.T) {
	e := newEnv(t)
	e.router.live["POST r"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseNode})
	finish := func(status domain.ExecutionStatus) {
		e.execs.start = func(execution.StartRequest) (domain.ExecutionID, error) {
			go func() {
				_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventExecutionFinished, ExecutionID: "exec_1", Status: status})
			}()
			return "exec_1", nil
		}
	}
	finish(domain.StatusSuccess)
	if resp, body := e.do(t, "POST", "/hook/r", ""); resp.StatusCode != 500 || !strings.Contains(body, "Respond to Webhook") {
		t.Fatalf("no respond node: %d %s", resp.StatusCode, body)
	}
	finish(domain.StatusWaiting)
	if resp, _ := e.do(t, "POST", "/hook/r", ""); resp.StatusCode != 202 {
		t.Fatalf("waiting: %d", resp.StatusCode)
	}
	e.execs.start = nil // nothing ever answers
	if resp, _ := e.do(t, "POST", "/hook/r", ""); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout: %d", resp.StatusCode)
	}
}

func TestIdempotencyKey(t *testing.T) {
	e := newEnv(t)
	e.router.live["POST i"] = route(nodesdk.WebhookSpec{})
	e.router.live["POST l"] = route(nodesdk.WebhookSpec{ResponseMode: nodesdk.ResponseLastNode})
	seen := map[string]domain.ExecutionID{}
	e.execs.start = func(r execution.StartRequest) (domain.ExecutionID, error) {
		if r.DedupKey == "" {
			return "exec_new", nil
		}
		if id, ok := seen[r.DedupKey]; ok {
			return id, &domain.DuplicateError{WorkflowID: r.WorkflowID, DedupKey: r.DedupKey, ExistingID: id}
		}
		seen[r.DedupKey] = "exec_first"
		return "exec_first", nil
	}
	e.do(t, "POST", "/hook/i", "", "Idempotency-Key", "k1")
	if got := e.lastRequest(t).DedupKey; got != "In:k1" {
		t.Fatalf("dedup key = %q", got)
	}
	resp, body := e.do(t, "POST", "/hook/i", "", "Idempotency-Key", "k1")
	if resp.StatusCode != 202 || jsonOf(t, body)["executionId"] != "exec_first" {
		t.Fatalf("repeat: %d %s", resp.StatusCode, body)
	}
	e.do(t, "POST", "/hook/i", "")
	if got := e.lastRequest(t).DedupKey; got != "" {
		t.Fatalf("no header, key = %q", got)
	}
	// lastNode: a repeat answers from the original execution
	e.execs.runSync = func(_ context.Context, r execution.StartRequest) (domain.Execution, error) {
		_, err := e.execs.start(r)
		return domain.Execution{}, err
	}
	e.execs.await = func(id domain.ExecutionID) (domain.Execution, error) {
		if id != "exec_first" {
			t.Errorf("await %s", id)
		}
		return domain.Execution{ID: id, Status: domain.StatusSuccess, RunData: map[string][]domain.NodeRun{"N": {{Output: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"a": "b"}}}}}}}}}, nil
	}
	e.do(t, "POST", "/hook/l", "", "Idempotency-Key", "k2")
	resp, body = e.do(t, "POST", "/hook/l", "", "Idempotency-Key", "k2")
	if resp.StatusCode != 200 || jsonOf(t, body)["a"] != "b" {
		t.Fatalf("lastNode repeat: %d %s", resp.StatusCode, body)
	}
}

func TestIdempotencyDisabledByConfig(t *testing.T) {
	e := newEnv(t, func(c *config.WebhookConfig) { c.IdempotencyHeader = "" })
	e.router.live["POST i"] = route(nodesdk.WebhookSpec{})
	e.do(t, "POST", "/hook/i", "", "Idempotency-Key", "k1")
	if got := e.lastRequest(t).DedupKey; got != "" {
		t.Fatalf("key = %q", got)
	}
}

func TestAuthentication(t *testing.T) {
	e := newEnv(t)
	basic := route(nodesdk.WebhookSpec{Auth: nodesdk.WebhookAuthBasic, AuthCredential: "basicAuth"})
	basic.Credential = "basic"
	hdr := route(nodesdk.WebhookSpec{Auth: nodesdk.WebhookAuthHeader, AuthCredential: "headerAuth"})
	hdr.Credential = "header"
	missing := route(nodesdk.WebhookSpec{Auth: nodesdk.WebhookAuthHeader})
	missing.Credential = "gone"
	e.router.live["POST basic"], e.router.live["POST header"], e.router.live["POST missing"] = basic, hdr, missing

	do := func(path string, set func(*http.Request)) int {
		req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(""))
		if set != nil {
			set(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("/hook/basic", nil); got != 401 {
		t.Fatalf("no credentials = %d", got)
	}
	if got := do("/hook/basic", func(r *http.Request) { r.SetBasicAuth("ann", "wrong") }); got != 401 {
		t.Fatalf("wrong password = %d", got)
	}
	if got := do("/hook/basic", func(r *http.Request) { r.SetBasicAuth("bob", "pw") }); got != 401 {
		t.Fatalf("wrong user = %d", got)
	}
	if len(e.execs.started) != 0 {
		t.Fatal("a refused call must not start an execution")
	}
	if got := do("/hook/basic", func(r *http.Request) { r.SetBasicAuth("ann", "pw") }); got != 202 {
		t.Fatalf("right credentials = %d", got)
	}
	if got := do("/hook/header", func(r *http.Request) { r.Header.Set("X-Token", "nope") }); got != 401 {
		t.Fatalf("wrong token = %d", got)
	}
	if got := do("/hook/header", func(r *http.Request) { r.Header.Set("X-Token", "s3cret") }); got != 202 {
		t.Fatalf("right token = %d", got)
	}
	if h := e.lastRequest(t).Items[0].JSON["headers"].(map[string]any); h["x-token"] != nil {
		t.Fatalf("the auth header was kept: %v", h)
	}
	if got := do("/hook/missing", nil); got != 401 {
		t.Fatalf("unknown credential = %d", got)
	}
}

func TestTestWebhookRunsInManualModeOnce(t *testing.T) {
	e := newEnv(t)
	rt := route(nodesdk.WebhookSpec{})
	rt.Mode, rt.Test = domain.ModeManual, true
	e.router.test["POST try"] = rt
	if resp, _ := e.do(t, "POST", "/hook-test/try", "", "Idempotency-Key", "k"); resp.StatusCode != 202 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	req := e.lastRequest(t)
	if req.Mode != domain.ModeManual || req.DedupKey != "" {
		t.Fatalf("request = %+v", req)
	}
	if resp, _ := e.do(t, "POST", "/hook-test/try", ""); resp.StatusCode != 404 {
		t.Fatalf("second call = %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/hook/try", ""); resp.StatusCode != 404 {
		t.Fatalf("a test route must not answer on the production path: %d", resp.StatusCode)
	}
}

func TestWaitResume(t *testing.T) {
	e := newEnv(t)
	resp, body := e.do(t, "POST", "/hook-wait/exec_1.secret?x=1", `{"approved":true}`, "Content-Type", "application/json")
	if resp.StatusCode != 200 || jsonOf(t, body)["resumed"] != true {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if e.resumer.token != "exec_1.secret" || len(e.resumer.items) != 1 {
		t.Fatalf("resumer = %+v", e.resumer)
	}
	j := e.resumer.items[0].JSON
	if j["body"].(map[string]any)["approved"] != true || j["query"].(map[string]any)["x"] != "1" || j["params"] != nil {
		t.Fatalf("item = %v", j)
	}
	if resp, _ := e.do(t, "GET", "/hook-wait/exec_1.secret", ""); resp.StatusCode != 200 {
		t.Fatalf("GET = %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "PUT", "/hook-wait/exec_1.secret", ""); resp.StatusCode != 405 {
		t.Fatalf("PUT = %d", resp.StatusCode)
	}
	for err, want := range map[error]int{waiter.ErrInvalidToken: 404, waiter.ErrNotWaiting: 409, errors.New("db down"): 500} {
		e.resumer.err = err
		if resp, body := e.do(t, "POST", "/hook-wait/exec_1.bad", ""); resp.StatusCode != want || strings.Contains(body, "db down") {
			t.Fatalf("%v: %d %s", err, resp.StatusCode, body)
		}
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := webhook.New(config.Default().Trigger.Webhook, webhook.Deps{}); err == nil {
		t.Fatal("want an error")
	}
	e := newEnv(t)
	if got := strings.Join(e.h.Patterns(), " "); got != "/hook/ /hook-test/ /hook-wait/" {
		t.Fatalf("patterns = %s", got)
	}
}
