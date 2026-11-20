package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ParamTools is the parameter of the MCP server trigger node that lists the
// workflows it exposes. The node validates it when the workflow is activated
// and the server reads it on every call, through ParseWorkflowTools.
const ParamTools = "tools"

// RouteMethod is the pseudo HTTP method under which an MCP server trigger
// registers its path in the webhook route table, so MCP paths share the
// uniqueness check and storage of webhook paths without ever colliding with a
// real method.
const RouteMethod = "MCP"

// Authentication kinds of an MCP server trigger (WebhookSpec.Auth).
const (
	ServerAuthNone   = "none"
	ServerAuthBearer = "bearer"
	ServerAuthHeader = "header"
)

// WorkflowTool exposes one workflow as an MCP tool.
type WorkflowTool struct {
	// Name is the tool name clients call; [a-zA-Z0-9_-]{1,64}.
	Name        string
	Description string
	// WorkflowID is the workflow to run. It must start with an Execute
	// Workflow Trigger node.
	WorkflowID string
	// InputSchema is the JSON Schema of the arguments (an object schema).
	InputSchema json.RawMessage
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// defaultInputSchema accepts any object, for tools that declare no schema.
const defaultInputSchema = `{"type":"object","additionalProperties":true}`

// ParseWorkflowTools reads the saved value of the tools parameter: a list of
// {name, description, workflowId, inputSchema} objects, where inputSchema is a
// JSON object or its text. It reports every problem it finds.
func ParseWorkflowTools(v any) ([]WorkflowTool, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("tools must be a list, got %T", v)
	}
	var tools []WorkflowTool
	var problems []string
	seen := map[string]bool{}
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("tool %d must be an object", i+1))
			continue
		}
		t := WorkflowTool{Name: text(m["name"]), Description: text(m["description"]), WorkflowID: text(m["workflowId"])}
		switch {
		case !toolName.MatchString(t.Name):
			problems = append(problems, fmt.Sprintf("tool %d: name %q must be 1-64 letters, digits, _ or -", i+1, t.Name))
		case seen[t.Name]:
			problems = append(problems, fmt.Sprintf("tool %d: name %q is used twice", i+1, t.Name))
		}
		seen[t.Name] = true
		if t.WorkflowID == "" {
			problems = append(problems, fmt.Sprintf("tool %d (%s): workflowId is required", i+1, t.Name))
		}
		schema, err := schemaOf(m["inputSchema"])
		if err != nil {
			problems = append(problems, fmt.Sprintf("tool %d (%s): inputSchema %v", i+1, t.Name, err))
		}
		t.InputSchema = schema
		tools = append(tools, t)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return tools, nil
}

func text(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// schemaOf accepts the schema as an object or as JSON text; empty means any
// object. MCP requires the root to be an object schema.
func schemaOf(v any) (json.RawMessage, error) {
	var raw []byte
	switch t := v.(type) {
	case nil:
		return json.RawMessage(defaultInputSchema), nil
	case string:
		if strings.TrimSpace(t) == "" {
			return json.RawMessage(defaultInputSchema), nil
		}
		raw = []byte(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, fmt.Errorf("is not JSON: %w", err)
		}
		raw = b
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("must be a JSON object: %w", err)
	}
	if typ, _ := obj["type"].(string); typ != "object" {
		return nil, fmt.Errorf(`must have "type": "object" at the root`)
	}
	return json.RawMessage(raw), nil
}
