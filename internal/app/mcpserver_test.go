package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/mcp"
)

// toolDoc is a workflow other workflows (and MCP clients) can call.
const toolDoc = `{
  "name": "double",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.executeWorkflow", "position": [0, 0]},
    {"name": "Calc", "type": "engi.transform.set", "position": [200, 0], "parameters": {"fields": [
      {"name": "double", "type": "number", "value": "={{ $json.n * 2 }}"}]}}
  ],
  "connections": [{"from": {"node": "Start"}, "to": {"node": "Calc"}}]
}`

const mcpDoc = `{
  "name": "tools",
  "nodes": [
    {"name": "MCP", "type": "engi.trigger.mcpServer", "position": [0, 0], "parameters": {"path": "calc", "tools": [
      {"name": "double", "description": "Doubles n", "workflowId": %q,
       "inputSchema": {"type": "object", "properties": {"n": {"type": "number"}}, "required": ["n"]}}]}}
  ],
  "connections": []
}`

// TestMCPServerTriggerEndToEnd activates a workflow with an MCP Server trigger
// and talks to /mcp/calc with the Engi MCP client, running a real workflow.
func TestMCPServerTriggerEndToEnd(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, p := range f.rt.MCP.Patterns() {
		mux.Handle(p, f.rt.MCP)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tool := f.save(t, toolDoc)
	wf := f.save(t, fmt.Sprintf(mcpDoc, tool.ID))

	cfg := mcp.ClientConfig{
		Endpoint: srv.URL + "/mcp/calc", Timeout: 10 * time.Second, MaxResponseBytes: 1 << 20, MaxListPages: 3, MaxTools: 10,
		Info: mcp.Implementation{Name: "test"},
	}
	if _, err := mcp.Connect(context.Background(), srv.Client(), cfg); err == nil {
		t.Fatal("an inactive workflow must not serve MCP")
	}
	if err := f.rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "double" || tools[0].Description != "Doubles n" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	res, err := c.CallTool(context.Background(), "double", json.RawMessage(`{"n": 21}`))
	if err != nil || res.IsError || res.Text() != `{"double":42,"n":21}` {
		t.Fatalf("call = %+v, %v", res, err)
	}
	if err := f.rt.Triggers.Deactivate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mcp.Connect(context.Background(), srv.Client(), cfg); err == nil {
		t.Fatal("a deactivated workflow must stop serving MCP")
	}
}

// TestMCPPathsAreUnique checks the MCP path shares the webhook uniqueness rule.
func TestMCPPathsAreUnique(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	tool := f.save(t, toolDoc)
	a := f.save(t, fmt.Sprintf(mcpDoc, tool.ID))
	b := f.save(t, fmt.Sprintf(mcpDoc, tool.ID))
	if err := f.rt.Triggers.Activate(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.Triggers.Activate(context.Background(), b.ID); err == nil {
		t.Fatal("two active workflows must not share an MCP path")
	}
}

// TestMCPRoutesAreNotReachableAsWebhooks guards the pseudo method: a client
// that sends a request with the method MCP to /hook/... must not reach the
// MCP trigger through the webhook handler.
func TestMCPRoutesAreNotReachableAsWebhooks(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	if err := f.rt.StartTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	tool := f.save(t, toolDoc)
	wf := f.save(t, fmt.Sprintf(mcpDoc, tool.ID))
	if err := f.rt.Triggers.Activate(context.Background(), wf.ID); err != nil {
		t.Fatal(err)
	}
	hooks := f.hookServer(t)
	req, _ := http.NewRequestWithContext(t.Context(), mcp.RouteMethod, hooks.URL+"/hook/calc", http.NoBody)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
