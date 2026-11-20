package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
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

type execs struct {
	mu   sync.Mutex
	reqs []execution.StartRequest
	run  func(context.Context, execution.StartRequest) (domain.Execution, error)
}

func (e *execs) RunSync(ctx context.Context, r execution.StartRequest) (domain.Execution, error) {
	e.mu.Lock()
	e.reqs = append(e.reqs, r)
	e.mu.Unlock()
	return e.run(ctx, r)
}

func success(items ...map[string]any) domain.Execution {
	out := make([]domain.Item, len(items))
	for i, m := range items {
		out[i] = domain.Item{JSON: m}
	}
	return domain.Execution{
		ID: "ex1", Status: domain.StatusSuccess,
		RunData: map[string][]domain.NodeRun{"Last": {{Status: domain.NodeRunSuccess, Output: domain.PortData{domain.PortMain: {out}}}}},
	}
}

type env struct {
	svc  *mcpserver.Service
	exec *execs
}

func setup(t *testing.T, cfgMut func(*config.MCPServerConfig)) env {
	t.Helper()
	tools := []any{
		map[string]any{"name": "greet", "description": "Greets", "workflowId": "wf-greet"},
		map[string]any{"name": "nostart", "workflowId": "wf-nostart"},
		map[string]any{"name": "gone", "workflowId": "wf-gone"},
	}
	wfs := workflows{
		"wf-mcp": {ID: "wf-mcp", Nodes: []domain.Node{{Name: "MCP", Type: "engi.trigger.mcpServer", Parameters: map[string]any{"tools": tools}}}},
		"wf-greet": {ID: "wf-greet", Nodes: []domain.Node{
			{Name: "Off", Type: subTrigger, Disabled: true},
			{Name: "Start", Type: subTrigger, Position: domain.Position{Y: 1}},
		}},
		"wf-nostart": {ID: "wf-nostart", Nodes: []domain.Node{{Name: "Manual", Type: "engi.trigger.manual"}}},
	}
	rt := trigger.Route{WorkflowID: "wf-mcp", Node: "MCP", Spec: nodesdk.WebhookSpec{Method: mcp.RouteMethod, Auth: "bearer"}, Credential: "cred1"}
	ex := &execs{run: func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return success(map[string]any{"hello": "world"}), nil
	}}
	cfg := config.Default().Trigger.MCPServer
	if cfgMut != nil {
		cfgMut(&cfg)
	}
	svc, err := mcpserver.New(cfg, mcpserver.Deps{
		Router: router{map[string]trigger.Route{"MCP tools": rt}}, Workflows: wfs, Executions: ex, SubworkflowTrigger: subTrigger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return env{svc, ex}
}

func open(t *testing.T, e env) *mcpserver.Endpoint {
	t.Helper()
	ep, err := e.svc.Open(context.Background(), "tools")
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func TestOpenAndList(t *testing.T) {
	e := setup(t, nil)
	if _, err := e.svc.Open(context.Background(), "other"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown path err = %v", err)
	}
	ep := open(t, e)
	if ep.Auth != "bearer" || ep.Credential != "cred1" {
		t.Errorf("auth = %q %q", ep.Auth, ep.Credential)
	}
	tools, _ := ep.ListTools(context.Background())
	if len(tools) != 3 || tools[0].Name != "greet" || tools[0].Description != "Greets" || string(tools[0].InputSchema) == "" {
		t.Errorf("tools = %+v", tools)
	}
}

func TestCallRunsTheWorkflow(t *testing.T) {
	e := setup(t, nil)
	res, err := open(t, e).CallTool(context.Background(), "greet", json.RawMessage(`{"name":"Ada"}`))
	if err != nil || res.IsError || res.Content[0].Text != `{"hello":"world"}` {
		t.Fatalf("res = %+v, %v", res, err)
	}
	req := e.exec.reqs[0]
	if req.WorkflowID != "wf-greet" || req.StartNode != "Start" || req.Mode != domain.ModeWebhook ||
		len(req.Items) != 1 || req.Items[0].JSON["name"] != "Ada" {
		t.Errorf("start request = %+v", req)
	}
}

func TestCallOutputShapes(t *testing.T) {
	e := setup(t, nil)
	e.exec.run = func(context.Context, execution.StartRequest) (domain.Execution, error) {
		return success(map[string]any{"a": 1}, map[string]any{"a": 2}), nil
	}
	res, _ := open(t, e).CallTool(context.Background(), "greet", nil)
	if res.Content[0].Text != `[{"a":1},{"a":2}]` {
		t.Errorf("many items = %s", res.Content[0].Text)
	}
	e.exec.run = func(context.Context, execution.StartRequest) (domain.Execution, error) { return success(), nil }
	if res, _ = open(t, e).CallTool(context.Background(), "greet", nil); res.IsError || res.Content[0].Text != "[]" {
		t.Errorf("no items = %+v", res)
	}
}

func TestToolFailuresAreResults(t *testing.T) {
	cases := map[string]struct {
		run     func(context.Context, execution.StartRequest) (domain.Execution, error)
		tool    string
		contain string
	}{
		"workflow failed": {func(context.Context, execution.StartRequest) (domain.Execution, error) {
			return domain.Execution{Status: domain.StatusError, Error: &domain.ErrorInfo{Message: "boom"}}, nil
		}, "greet", "boom"},
		"waiting": {func(context.Context, execution.StartRequest) (domain.Execution, error) {
			return domain.Execution{ID: "w1", Status: domain.StatusWaiting}, nil
		}, "greet", "waiting"},
		"timeout": {func(context.Context, execution.StartRequest) (domain.Execution, error) {
			return domain.Execution{}, fmt.Errorf("x: %w", execution.ErrSyncTimeout)
		}, "greet", "in time"},
		"internal error is hidden": {func(context.Context, execution.StartRequest) (domain.Execution, error) {
			return domain.Execution{}, errors.New("db password leaked")
		}, "greet", "could not be run"},
		"no start node":    {nil, "nostart", "Execute Workflow Trigger"},
		"missing workflow": {nil, "gone", "does not exist"},
	}
	for name, tc := range cases {
		e := setup(t, nil)
		if tc.run != nil {
			e.exec.run = tc.run
		}
		res, err := open(t, e).CallTool(context.Background(), tc.tool, nil)
		if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, tc.contain) {
			t.Errorf("%s: res = %+v, err = %v", name, res, err)
		}
		if strings.Contains(res.Content[0].Text, "password") {
			t.Errorf("%s leaked internals", name)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	ep := open(t, setup(t, nil))
	if _, err := ep.CallTool(context.Background(), "nope", nil); !errors.Is(err, mcp.ErrUnknownTool) {
		t.Errorf("unknown tool err = %v", err)
	}
	for _, args := range []string{`[1]`, `"x"`, `5`} {
		_, err := ep.CallTool(context.Background(), "greet", json.RawMessage(args))
		var rpc *mcp.RPCError
		if !errors.As(err, &rpc) || rpc.Code != mcp.CodeInvalidParams {
			t.Errorf("args %s err = %v", args, err)
		}
	}
}

func TestConcurrencyLimitAndTimeout(t *testing.T) {
	e := setup(t, func(c *config.MCPServerConfig) { c.MaxConcurrentCalls = 1; c.CallTimeout = time.Second })
	started, release := make(chan struct{}), make(chan struct{})
	e.exec.run = func(ctx context.Context, _ execution.StartRequest) (domain.Execution, error) {
		close(started)
		select {
		case <-release:
			return success(map[string]any{}), nil
		case <-ctx.Done():
			return domain.Execution{}, ctx.Err()
		}
	}
	ep := open(t, e)
	done := make(chan mcp.CallToolResult)
	go func() { r, _ := ep.CallTool(context.Background(), "greet", nil); done <- r }()
	<-started
	_, err := ep.CallTool(context.Background(), "greet", nil)
	var rpc *mcp.RPCError
	if !errors.As(err, &rpc) || rpc.Code != mcp.CodeServerBusy {
		t.Errorf("second call err = %v, want server busy", err)
	}
	close(release)
	if r := <-done; r.IsError {
		t.Errorf("first call = %+v", r)
	}

	// A workflow that outlives callTimeout is cancelled and reported.
	e2 := setup(t, func(c *config.MCPServerConfig) { c.CallTimeout = 20 * time.Millisecond })
	e2.exec.run = func(ctx context.Context, _ execution.StartRequest) (domain.Execution, error) {
		<-ctx.Done()
		return domain.Execution{}, ctx.Err()
	}
	res, err := open(t, e2).CallTool(context.Background(), "greet", nil)
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "in time") {
		t.Errorf("timeout res = %+v, err = %v", res, err)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := mcpserver.New(config.Default().Trigger.MCPServer, mcpserver.Deps{}); err == nil {
		t.Error("expected an error for missing dependencies")
	}
}
