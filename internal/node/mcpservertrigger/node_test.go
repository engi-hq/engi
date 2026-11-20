package mcpservertrigger_test

import (
	"context"
	"testing"

	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/node/mcpservertrigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func tool(name string) map[string]any {
	return map[string]any{"name": name, "workflowId": "wf-" + name}
}

func TestDescriptor(t *testing.T) {
	if err := mcpservertrigger.New().Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookSpec(t *testing.T) {
	n := mcpservertrigger.New().(nodesdk.WebhookTrigger)
	spec, err := n.Webhook(map[string]any{"path": "/tools/", "authentication": "bearer", "tools": []any{tool("a")}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Method != mcp.RouteMethod || spec.Path != "tools" || spec.Auth != "bearer" || spec.AuthCredential != "bearerAuth" {
		t.Errorf("spec = %+v", spec)
	}
	spec, err = n.Webhook(map[string]any{"authentication": "header", "tools": []any{tool("a")}})
	if err != nil || spec.AuthCredential != "headerAuth" || spec.Path != "" {
		t.Errorf("header spec = %+v, %v", spec, err)
	}
	if spec, _ = n.Webhook(map[string]any{"tools": []any{tool("a")}}); spec.Auth != "none" || spec.AuthCredential != "" {
		t.Errorf("default auth = %+v", spec)
	}
	for name, params := range map[string]map[string]any{
		"no tools":     {},
		"empty tools":  {"tools": []any{}},
		"bad tool":     {"tools": []any{map[string]any{"name": "x"}}},
		"param path":   {"path": "a/:id", "tools": []any{tool("a")}},
		"unknown auth": {"authentication": "basic", "tools": []any{tool("a")}},
	} {
		if _, err := n.Webhook(params); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNeverRuns(t *testing.T) {
	if _, err := mcpservertrigger.New().Execute(context.Background(), nil); err == nil {
		t.Error("the trigger must not execute")
	}
}
