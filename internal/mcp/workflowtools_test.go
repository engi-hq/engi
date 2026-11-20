package mcp_test

import (
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/mcp"
)

func TestParseWorkflowTools(t *testing.T) {
	tools, err := mcp.ParseWorkflowTools([]any{
		map[string]any{"name": "a", "workflowId": "wf1", "description": "d"},
		map[string]any{"name": "b", "workflowId": "wf2", "inputSchema": `{"type":"object","properties":{"q":{"type":"string"}}}`},
		map[string]any{"name": "c", "workflowId": "wf3", "inputSchema": map[string]any{"type": "object"}},
	})
	if err != nil || len(tools) != 3 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if !strings.Contains(string(tools[0].InputSchema), `"object"`) || !strings.Contains(string(tools[1].InputSchema), `"q"`) {
		t.Errorf("schemas = %s / %s", tools[0].InputSchema, tools[1].InputSchema)
	}
	for name, in := range map[string]any{
		"not a list":      "x",
		"bad name":        []any{map[string]any{"name": "has space", "workflowId": "w"}},
		"duplicate":       []any{map[string]any{"name": "a", "workflowId": "w"}, map[string]any{"name": "a", "workflowId": "w"}},
		"no workflow":     []any{map[string]any{"name": "a"}},
		"array schema":    []any{map[string]any{"name": "a", "workflowId": "w", "inputSchema": `{"type":"array"}`}},
		"schema not JSON": []any{map[string]any{"name": "a", "workflowId": "w", "inputSchema": `{`}},
	} {
		if _, err := mcp.ParseWorkflowTools(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if got, err := mcp.ParseWorkflowTools(nil); err != nil || got != nil {
		t.Errorf("nil = %v, %v", got, err)
	}
}
