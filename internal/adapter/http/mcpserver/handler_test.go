package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpmcp "github.com/engi-hq/engi/internal/adapter/http/mcpserver"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/node/aitoolmcp"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/mcpserver"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const subTrigger = "engi.trigger.executeWorkflow"

type router struct{ routes map[string]trigger.Route }

func (r router) ResolveMCP(_ context.Context, path string) (trigger.Route, error) {
	if rt, ok := r.routes["MCP "+path]; ok {
		return rt, nil
	}
	return trigger.Route{}, fmt.Errorf("no route: %w", domain.ErrNotFound)
}

type workflows map[domain.WorkflowID]domain.Workflow

func (w workflows) Get(_ context.Context, id domain.WorkflowID) (domain.Workflow, error) {
	if wf, ok := w[id]; ok {
		return wf, nil
	}
	return domain.Workflow{}, fmt.Errorf("workflow %s: %w", id, domain.ErrNotFound)
}

// execs plays the engine: the "add" workflow sums a and b.
type execs struct{ started []execution.StartRequest }

func (e *execs) RunSync(_ context.Context, r execution.StartRequest) (domain.Execution, error) {
	e.started = append(e.started, r)
	a, _ := r.Items[0].JSON["a"].(float64)
	b, _ := r.Items[0].JSON["b"].(float64)
	out := domain.PortData{domain.PortMain: {{{JSON: map[string]any{"sum": a + b}}}}}
	return domain.Execution{ID: "ex", Status: domain.StatusSuccess,
		RunData: map[string][]domain.NodeRun{"Sum": {{Status: domain.NodeRunSuccess, Output: out}}}}, nil
}

type creds map[domain.CredentialID]map[string]any

func (c creds) Resolve(_ context.Context, id domain.CredentialID) (port.CredentialData, error) {
	d, ok := c[id]
	if !ok {
		return port.CredentialData{}, domain.ErrNotFound
	}
	return port.CredentialData{Data: d}, nil
}

type fixture struct {
	srv   *httptest.Server
	execs *execs
}

func newFixture(t *testing.T, mut func(*config.MCPServerConfig)) fixture {
	t.Helper()
	tools := []any{map[string]any{
		"name": "add", "description": "Adds a and b", "workflowId": "wf-add",
		"inputSchema": `{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}}}`,
	}}
	node := func(auth string) domain.Node {
		return domain.Node{Name: "MCP", Type: "engi.trigger.mcpServer", Parameters: map[string]any{"tools": tools, "authentication": auth}}
	}
	wfs := workflows{
		"wf-open":   {ID: "wf-open", Nodes: []domain.Node{node("none")}},
		"wf-bearer": {ID: "wf-bearer", Nodes: []domain.Node{node("bearer")}},
		"wf-header": {ID: "wf-header", Nodes: []domain.Node{node("header")}},
		"wf-add":    {ID: "wf-add", Nodes: []domain.Node{{Name: "Start", Type: subTrigger}}},
	}
	route := func(wf domain.WorkflowID, auth, cred string) trigger.Route {
		return trigger.Route{WorkflowID: wf, Node: "MCP", Spec: nodesdk.WebhookSpec{Auth: auth}, Credential: domain.CredentialID(cred)}
	}
	rt := router{map[string]trigger.Route{
		"MCP open":   route("wf-open", "none", ""),
		"MCP bearer": route("wf-bearer", "bearer", "c-bearer"),
		"MCP header": route("wf-header", "header", "c-header"),
	}}
	cfg := config.Default().Trigger.MCPServer
	if mut != nil {
		mut(&cfg)
	}
	ex := &execs{}
	svc, err := mcpserver.New(cfg, mcpserver.Deps{Router: rt, Workflows: wfs, Executions: ex, SubworkflowTrigger: subTrigger})
	if err != nil {
		t.Fatal(err)
	}
	h, err := httpmcp.New(cfg, httpmcp.Deps{
		Endpoints: svc,
		Credentials: creds{
			"c-bearer": {"token": "s3cret"},
			"c-header": {"name": "X-Api-Key", "value": "k3y"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, p := range h.Patterns() {
		mux.Handle(p, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fixture{srv, ex}
}

func (f fixture) post(t *testing.T, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	return m
}

const (
	initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t"}}}`
	listBody = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	callBody = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":2,"b":3}}}`
)

func TestInitializeListAndCall(t *testing.T) {
	f := newFixture(t, nil)
	resp := f.post(t, "open", initBody, nil)
	res := decode(t, resp)["result"].(map[string]any)
	if resp.StatusCode != 200 || res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "engi" {
		t.Errorf("initialize = %d %v", resp.StatusCode, res)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	tools := decode(t, f.post(t, "open", listBody, nil))["result"].(map[string]any)["tools"].([]any)
	first := tools[0].(map[string]any)
	if len(tools) != 1 || first["name"] != "add" || first["description"] != "Adds a and b" || first["inputSchema"].(map[string]any)["type"] != "object" {
		t.Errorf("tools = %v", tools)
	}
	out := decode(t, f.post(t, "/open/", callBody, nil))["result"].(map[string]any)
	if out["isError"] == true || out["content"].([]any)[0].(map[string]any)["text"] != `{"sum":5}` {
		t.Errorf("call = %v", out)
	}
	if len(f.execs.started) != 1 || f.execs.started[0].StartNode != "Start" {
		t.Errorf("started = %+v", f.execs.started)
	}
}

func TestNotificationIsAccepted(t *testing.T) {
	f := newFixture(t, nil)
	resp := f.post(t, "open", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestRoutingAndMethods(t *testing.T) {
	f := newFixture(t, nil)
	if resp := f.post(t, "nowhere", listBody, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path = %d", resp.StatusCode)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequest(method, f.srv.URL+"/mcp/open", http.NoBody)
		resp, err := f.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "POST" {
			t.Errorf("%s = %d (Allow %q)", method, resp.StatusCode, resp.Header.Get("Allow"))
		}
	}
	if resp := f.post(t, "open", listBody, map[string]string{mcp.HeaderProtocolVersion: "1999-01-01"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad protocol version = %d", resp.StatusCode)
	}
	if resp := f.post(t, "open", `{bad`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d", resp.StatusCode)
	}
}

func TestAuthentication(t *testing.T) {
	f := newFixture(t, nil)
	for name, tc := range map[string]struct {
		path string
		hdr  map[string]string
		want int
	}{
		"bearer ok":        {"bearer", map[string]string{"Authorization": "Bearer s3cret"}, 200},
		"bearer lowercase": {"bearer", map[string]string{"Authorization": "bearer s3cret"}, 200},
		"bearer missing":   {"bearer", nil, 401},
		"bearer wrong":     {"bearer", map[string]string{"Authorization": "Bearer nope"}, 401},
		"bearer basic":     {"bearer", map[string]string{"Authorization": "Basic czNjcmV0"}, 401},
		"header ok":        {"header", map[string]string{"X-Api-Key": "k3y"}, 200},
		"header wrong":     {"header", map[string]string{"X-Api-Key": "x"}, 401},
		"header missing":   {"header", nil, 401},
	} {
		resp := f.post(t, tc.path, listBody, tc.hdr)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", name, resp.StatusCode, tc.want)
		}
		if tc.want == 401 && tc.path == "bearer" && resp.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: WWW-Authenticate = %q", name, resp.Header.Get("WWW-Authenticate"))
		}
	}
	if len(f.execs.started) != 0 {
		t.Error("an unauthenticated call started a workflow")
	}
}

func TestBodyLimit(t *testing.T) {
	f := newFixture(t, func(c *config.MCPServerConfig) { c.MaxBodyBytes = 64 })
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", 200) + `"}}`
	if resp := f.post(t, "open", big, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestOriginCheck(t *testing.T) {
	f := newFixture(t, func(c *config.MCPServerConfig) { c.AllowedOrigins = []string{"https://app.example.com"} })
	if resp := f.post(t, "open", listBody, map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin = %d", resp.StatusCode)
	}
	if resp := f.post(t, "open", listBody, map[string]string{"Origin": "https://app.example.com"}); resp.StatusCode != 200 {
		t.Errorf("allowed origin = %d", resp.StatusCode)
	}
	if resp := f.post(t, "open", listBody, nil); resp.StatusCode != 200 {
		t.Errorf("no origin = %d", resp.StatusCode)
	}
}

func TestToolErrorsAreJSONRPC(t *testing.T) {
	f := newFixture(t, nil)
	e := decode(t, f.post(t, "open", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ghost"}}`, nil))["error"].(map[string]any)
	if e["code"] != float64(mcp.CodeInvalidParams) {
		t.Errorf("unknown tool error = %v", e)
	}
	e = decode(t, f.post(t, "open", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add","arguments":[1]}}`, nil))["error"].(map[string]any)
	if e["code"] != float64(mcp.CodeInvalidParams) {
		t.Errorf("bad arguments error = %v", e)
	}
}

// TestEngiClientToolTalksToEngiServer drives the MCP client tool node against
// the real handler: list, then call, over the wire.
func TestEngiClientToolTalksToEngiServer(t *testing.T) {
	f := newFixture(t, nil)
	n := aitoolmcp.New(aitoolmcp.Config{
		Limits: aitoolmcp.Limits{RequestTimeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxListPages: 3, MaxTools: 10, MaxResultBytes: 1 << 10},
		Info:   mcp.Implementation{Name: "engi-test"},
	})
	in := nodetest.New(n.Describe(), nil, map[string]any{"endpoint": f.srv.URL + "/mcp/bearer", "authentication": "credential"})
	in.Client = f.srv.Client()
	in.Credentials = map[string]nodesdk.Credential{"engi.mcp": {Data: map[string]any{"authentication": "bearer", "token": "s3cret"}}}
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := v.(port.ToolProvider).Tools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Definition().Name != "add" {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if got := string(tools[0].Definition().Parameters); !strings.Contains(got, `"a"`) {
		t.Errorf("schema = %s", got)
	}
	res, err := tools[0].Call(context.Background(), json.RawMessage(`{"a":40,"b":2}`))
	if err != nil || res.IsError || res.Content != `{"sum":42}` {
		t.Errorf("call = %+v, %v", res, err)
	}

	// Wrong token: the node surfaces the 401 as an error.
	in.Credentials = map[string]nodesdk.Credential{"engi.mcp": {Data: map[string]any{"authentication": "bearer", "token": "wrong"}}}
	v, _ = n.Supply(context.Background(), in)
	if _, err := v.(port.ToolProvider).Tools(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token err = %v", err)
	}
}
