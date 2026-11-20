package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/http/webhook"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type chatRouter map[string]trigger.ChatRoute

func (c chatRouter) ResolveChat(_ context.Context, path string) (trigger.ChatRoute, error) {
	if r, ok := c[path]; ok {
		return r, nil
	}
	return trigger.ChatRoute{}, domain.ErrNotFound
}

type chatEnv struct {
	router chatRouter
	execs  *execs
	bus    *servicetest.Bus
	srv    *httptest.Server
}

func newChatEnv(t *testing.T) *chatEnv {
	t.Helper()
	cfg := config.Default().Trigger
	cfg.Chat.MaxBodyBytes = 200
	cfg.Chat.ResponseTimeout = 300 * time.Millisecond
	e := &chatEnv{router: chatRouter{}, execs: &execs{}, bus: servicetest.NewBus()}
	h, err := webhook.New(cfg.Webhook, webhook.Deps{
		Router: router{}, Executions: e.execs, Resumer: &resumer{}, Bus: e.bus,
		Credentials: creds{"basic": {"user": "ann", "password": "pw"}, "header": {"name": "X-Token", "value": "s3cret"}},
		Chat:        &webhook.ChatDeps{Config: cfg.Chat, Router: e.router, SessionIDs: func() (string, error) { return "minted", nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, p := range h.Patterns() {
		mux.Handle(p, h)
	}
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func chatRoute(spec nodesdk.ChatSpec) trigger.ChatRoute {
	if spec.ResponseMode == "" {
		spec.ResponseMode = nodesdk.ResponseLastNode
	}
	if spec.Auth == "" {
		spec.Auth = nodesdk.WebhookAuthNone
	}
	if spec.ReplyField == "" {
		spec.ReplyField = "output"
	}
	cred := domain.CredentialID("")
	switch spec.Auth {
	case nodesdk.WebhookAuthBasic:
		cred = "basic"
	case nodesdk.WebhookAuthHeader:
		cred = "header"
	}
	return trigger.ChatRoute{
		Route: trigger.Route{WorkflowID: "wf_1", Node: "Chat", Mode: domain.ModeWebhook, Credential: cred,
			Spec: nodesdk.WebhookSpec{Method: nodesdk.WebhookMethodChat, ResponseMode: spec.ResponseMode, Auth: spec.Auth}},
		Chat: spec,
	}
}

func (e *chatEnv) do(t *testing.T, method, path, body string, hdr ...string) (resp *http.Response, text string) {
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
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, sb.String()
}

func lastNodeAnswer(items ...map[string]any) func(context.Context, execution.StartRequest) (domain.Execution, error) {
	return func(context.Context, execution.StartRequest) (domain.Execution, error) {
		out := make([]domain.Item, len(items))
		for i, it := range items {
			out[i] = domain.Item{JSON: it}
		}
		return domain.Execution{ID: "exec_1", Status: domain.StatusSuccess, RunData: map[string][]domain.NodeRun{
			"Agent": {{Status: domain.NodeRunSuccess, Output: domain.PortData{domain.PortMain: {out}}}},
		}}, nil
	}
}

func TestChatMessageStartsTheWorkflowAndReplies(t *testing.T) {
	e := newChatEnv(t)
	e.router["support"] = chatRoute(nodesdk.ChatSpec{})
	e.execs.runSync = lastNodeAnswer(map[string]any{"output": "hello there", "other": 1})

	resp, body := e.do(t, "POST", "/chat/support", `{"sessionId":"s-1","message":"hi","metadata":{"page":"/x"}}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := jsonOf(t, body)
	if got["output"] != "hello there" || got["sessionId"] != "s-1" || resp.Header.Get("X-Chat-Session-Id") != "s-1" {
		t.Fatalf("answer = %s, headers %v", body, resp.Header)
	}
	req := e.execs.started[0]
	item := req.Items[0].JSON
	meta, _ := item["metadata"].(map[string]any)
	if req.StartNode != "Chat" || req.WorkflowID != "wf_1" || req.Mode != domain.ModeWebhook ||
		item["sessionId"] != "s-1" || item["chatInput"] != "hi" || meta["page"] != "/x" {
		t.Fatalf("start request = %+v", req)
	}
}

func TestChatSessionIDIsGeneratedAndChatInputIsAnAlias(t *testing.T) {
	e := newChatEnv(t)
	e.router["support"] = chatRoute(nodesdk.ChatSpec{})
	e.execs.runSync = lastNodeAnswer(map[string]any{"output": "ok"})

	resp, body := e.do(t, "POST", "/chat/support", `{"chatInput":"hello"}`)
	if got := jsonOf(t, body); resp.StatusCode != 200 || got["sessionId"] != "minted" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	item := e.execs.started[0].Items[0].JSON
	if item["chatInput"] != "hello" || item["sessionId"] != "minted" {
		t.Fatalf("item = %v", item)
	}
	if _, has := item["metadata"]; has {
		t.Fatalf("metadata set without being sent: %v", item)
	}
}

func TestChatRejectsBadRequests(t *testing.T) {
	e := newChatEnv(t)
	e.router["support"] = chatRoute(nodesdk.ChatSpec{})
	e.execs.runSync = lastNodeAnswer(map[string]any{"output": "ok"})
	tests := []struct {
		name, method, path, body string
		want                     int
	}{
		{"unknown path", "POST", "/chat/nope", `{"message":"x"}`, 404},
		{"not json", "POST", "/chat/support", `nope`, 400},
		{"no message", "POST", "/chat/support", `{"sessionId":"a"}`, 400},
		{"blank message", "POST", "/chat/support", `{"message":"   "}`, 400},
		{"message not a string", "POST", "/chat/support", `{"message":5}`, 400},
		{"bad session id", "POST", "/chat/support", `{"message":"x","sessionId":"a b"}`, 400},
		{"long session id", "POST", "/chat/support", `{"message":"x","sessionId":"` + strings.Repeat("a", 129) + `"}`, 400},
		{"too large", "POST", "/chat/support", `{"message":"` + strings.Repeat("a", 300) + `"}`, 413},
		{"wrong method", "GET", "/chat/support", ``, 405},
		{"empty path", "POST", "/chat/", `{"message":"x"}`, 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := e.do(t, tt.method, tt.path, tt.body)
			if resp.StatusCode != tt.want {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tt.want, body)
			}
		})
	}
	if len(e.execs.started) != 0 {
		t.Fatalf("rejected requests started %d executions", len(e.execs.started))
	}
}

func TestChatAuthModes(t *testing.T) {
	e := newChatEnv(t)
	e.router["basic"] = chatRoute(nodesdk.ChatSpec{Auth: nodesdk.WebhookAuthBasic})
	e.router["header"] = chatRoute(nodesdk.ChatSpec{Auth: nodesdk.WebhookAuthHeader})
	e.execs.runSync = lastNodeAnswer(map[string]any{"output": "ok"})
	msg := `{"message":"x"}`

	if resp, _ := e.do(t, "POST", "/chat/basic", msg); resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("basic without credentials: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/basic", msg, "Authorization", "Basic YW5uOm53"); resp.StatusCode != 401 {
		t.Fatalf("basic wrong password: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/chat/basic", msg, "Authorization", "Basic YW5uOnB3"); resp.StatusCode != 200 {
		t.Fatalf("basic right: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/chat/header", msg); resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("header without token: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/header", msg, "X-Token", "s3cret"); resp.StatusCode != 200 {
		t.Fatalf("header right: %d", resp.StatusCode)
	}
	if len(e.execs.started) != 2 {
		t.Fatalf("started %d executions, want 2", len(e.execs.started))
	}
}

func TestChatCORS(t *testing.T) {
	e := newChatEnv(t)
	e.router["closed"] = chatRoute(nodesdk.ChatSpec{})
	e.router["open"] = chatRoute(nodesdk.ChatSpec{AllowedOrigins: []string{"https://shop.example"}})
	e.router["any"] = chatRoute(nodesdk.ChatSpec{AllowedOrigins: []string{"*"}})
	e.execs.runSync = lastNodeAnswer(map[string]any{"output": "ok"})
	msg := `{"message":"x"}`

	resp, _ := e.do(t, "POST", "/chat/open", msg, "Origin", "https://shop.example")
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "https://shop.example" || resp.Header.Get("Vary") != "Origin" ||
		resp.Header.Get("Access-Control-Expose-Headers") != "X-Chat-Session-Id" {
		t.Fatalf("allowed origin: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/open", msg, "Origin", "https://evil.example"); resp.StatusCode != 403 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/closed", msg, "Origin", "https://shop.example"); resp.StatusCode != 403 {
		t.Fatalf("no origins configured: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/chat/closed", msg, "Origin", e.srv.URL); resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("same origin: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/closed", msg); resp.StatusCode != 200 {
		t.Fatalf("no Origin header (server to server): %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/chat/any", msg, "Origin", "https://whoever.example"); resp.StatusCode != 200 ||
		resp.Header.Get("Access-Control-Allow-Origin") != "https://whoever.example" {
		t.Fatalf("wildcard: %d %v", resp.StatusCode, resp.Header)
	}

	resp, _ = e.do(t, "OPTIONS", "/chat/open", "", "Origin", "https://shop.example", "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "content-type")
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" ||
		resp.Header.Get("Access-Control-Allow-Headers") != "content-type" || resp.Header.Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.do(t, "OPTIONS", "/chat/open", "", "Origin", "https://evil.example"); resp.StatusCode != 403 {
		t.Fatalf("preflight from foreign origin: %d", resp.StatusCode)
	}
	if len(e.execs.started) != 4 {
		t.Fatalf("started %d executions, want 4", len(e.execs.started))
	}
}

func TestChatReplyExtraction(t *testing.T) {
	tests := []struct {
		name  string
		field string
		items []map[string]any
		want  string
		none  bool // no reply can be found: 502
	}{
		{"configured field", "output", []map[string]any{{"output": "a", "text": "b"}}, "a", false},
		{"falls back to text", "output", []map[string]any{{"text": "b"}}, "b", false},
		{"custom field", "answer", []map[string]any{{"answer": "c"}}, "c", false},
		{"first item only", "output", []map[string]any{{"output": "first"}, {"output": "second"}}, "first", false},
		{"non-string becomes json", "output", []map[string]any{{"output": map[string]any{"k": 1.0}}}, `{"k":1}`, false},
		{"empty string is a reply", "output", []map[string]any{{"output": ""}}, "", false},
		{"nothing to say", "output", []map[string]any{{"x": 1}}, "", true},
		{"no items", "output", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newChatEnv(t)
			e.router["c"] = chatRoute(nodesdk.ChatSpec{ReplyField: tt.field})
			e.execs.runSync = lastNodeAnswer(tt.items...)
			resp, body := e.do(t, "POST", "/chat/c", `{"message":"x"}`)
			if tt.none {
				if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "no reply") {
					t.Fatalf("%d %s", resp.StatusCode, body)
				}
				return
			}
			if got := jsonOf(t, body); resp.StatusCode != 200 || got["output"] != tt.want {
				t.Fatalf("%d %s", resp.StatusCode, body)
			}
		})
	}
}

func TestChatFailureModes(t *testing.T) {
	e := newChatEnv(t)
	e.router["c"] = chatRoute(nodesdk.ChatSpec{})
	e.execs.runSync = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return domain.Execution{ID: "exec_1", Status: domain.StatusError}, nil
	}
	if resp, _ := e.do(t, "POST", "/chat/c", `{"message":"x"}`); resp.StatusCode != 500 {
		t.Fatalf("failed run: %d", resp.StatusCode)
	}
	e.execs.runSync = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return domain.Execution{ID: "exec_1", Status: domain.StatusWaiting}, nil
	}
	if resp, body := e.do(t, "POST", "/chat/c", `{"message":"x","sessionId":"s"}`); resp.StatusCode != 202 || jsonOf(t, body)["sessionId"] != "s" {
		t.Fatalf("waiting run: %d %s", resp.StatusCode, body)
	}
	e.execs.runSync = func(ctx context.Context, _ execution.StartRequest) (domain.Execution, error) {
		<-ctx.Done()
		return domain.Execution{}, ctx.Err()
	}
	if resp, _ := e.do(t, "POST", "/chat/c", `{"message":"x"}`); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout: %d", resp.StatusCode)
	}
}

func TestChatResponseNodeMode(t *testing.T) {
	e := newChatEnv(t)
	e.router["r"] = chatRoute(nodesdk.ChatSpec{ResponseMode: nodesdk.ResponseNode})
	e.execs.start = func(execution.StartRequest) (domain.ExecutionID, error) {
		go func() {
			_ = e.bus.Publish(context.Background(), port.Event{Kind: port.EventNodeFinished, ExecutionID: "exec_1", Run: &domain.NodeRun{
				Response: &domain.WebhookResponse{Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"output":"via node"}`)}}})
		}()
		return "exec_1", nil
	}
	resp, body := e.do(t, "POST", "/chat/r", `{"message":"x"}`)
	if resp.StatusCode != 200 || jsonOf(t, body)["output"] != "via node" || resp.Header.Get("X-Chat-Session-Id") != "minted" {
		t.Fatalf("%d %s %v", resp.StatusCode, body, resp.Header)
	}
	// nothing answers: the chat timeout applies
	e.execs.start = nil
	if resp, _ := e.do(t, "POST", "/chat/r", `{"message":"x"}`); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout: %d", resp.StatusCode)
	}
}

func TestChatIsOffWithoutDeps(t *testing.T) {
	e := newEnv(t)
	for _, p := range e.h.Patterns() {
		if strings.HasPrefix(p, "/chat") {
			t.Fatalf("pattern %q served without chat deps", p)
		}
	}
	if _, err := webhook.New(config.Default().Trigger.Webhook, webhook.Deps{
		Router: router{}, Executions: &execs{}, Resumer: &resumer{}, Bus: servicetest.NewBus(), Credentials: creds{}, Chat: &webhook.ChatDeps{},
	}); err == nil {
		t.Fatal("want an error for chat deps without a router")
	}
}

func TestWidgetPage(t *testing.T) {
	e := newChatEnv(t)
	e.router["support"] = chatRoute(nodesdk.ChatSpec{
		Title: `Help <b>desk</b>`, Welcome: `Hi "friend" & <script>alert(1)</script>`, Placeholder: `Ask" onfocus="x`,
		AllowedOrigins: []string{"https://shop.example"},
	})
	e.router["private"] = chatRoute(nodesdk.ChatSpec{Auth: nodesdk.WebhookAuthBasic})
	e.router["a/b"] = chatRoute(nodesdk.ChatSpec{})

	resp, body := e.do(t, "GET", "/chat/support/widget", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors https://shop.example"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", resp.Header)
	}
	for _, bad := range []string{"<script>alert", "<b>desk", `"friend"`, `onfocus="x`} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains unescaped %q", bad)
		}
	}
	for _, want := range []string{"Help &lt;b&gt;desk&lt;/b&gt;", "&lt;script&gt;alert(1)&lt;/script&gt;", `src="widget.js"`, `href="widget.css"`, `aria-live="polite"`, `<label class="visually-hidden" for="chat-input">`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Count(body, "<script") != 1 || strings.Contains(body, "<style") || strings.Contains(body, " style=") || strings.Contains(body, " onclick") {
		t.Errorf("page has inline script or style: %s", body)
	}

	if resp, _ := e.do(t, "GET", "/chat/nope/widget", ""); resp.StatusCode != 404 {
		t.Errorf("unknown chat: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/chat/private/widget", ""); resp.StatusCode != 403 {
		t.Errorf("authenticated chat: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "GET", "/chat/a/b/widget", ""); resp.StatusCode != 200 {
		t.Errorf("nested path: %d", resp.StatusCode)
	}
	if resp, _ := e.do(t, "POST", "/chat/support/widget", `{}`); resp.StatusCode != 405 {
		t.Errorf("POST widget: %d", resp.StatusCode)
	}
	if resp, csp := e.do(t, "GET", "/chat/a/b/widget", ""); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Errorf("default ancestors: %v %s", resp.Header, csp)
	}
}

func TestWidgetAssets(t *testing.T) {
	e := newChatEnv(t)
	cases := []struct{ path, ct, contains string }{
		{"/chat/support/widget.js", "text/javascript", "sessionStorage"},
		{"/chat/support/widget.css", "text/css", "prefers-color-scheme"},
		{"/chat/support/loader.js", "text/javascript", "iframe"},
	}
	for _, c := range cases {
		resp, body := e.do(t, "GET", c.path, "")
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), c.ct) || !strings.Contains(body, c.contains) ||
			resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("ETag") == "" {
			t.Errorf("%s: %d %v", c.path, resp.StatusCode, resp.Header)
		}
		if strings.Contains(body, "innerHTML") || strings.Contains(body, "eval(") {
			t.Errorf("%s uses an unsafe sink", c.path)
		}
		again, _ := e.do(t, "GET", c.path, "", "If-None-Match", resp.Header.Get("ETag"))
		if again.StatusCode != http.StatusNotModified {
			t.Errorf("%s revalidation: %d", c.path, again.StatusCode)
		}
	}
	if resp, _ := e.do(t, "GET", "/chat/support/loader.js", ""); resp.Header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("loader headers = %v", resp.Header)
	}
	if resp, _ := e.do(t, "POST", "/chat/support/widget.js", `{}`); resp.StatusCode != 404 {
		t.Errorf("POST asset: %d", resp.StatusCode)
	}
}
