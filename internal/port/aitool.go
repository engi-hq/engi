package port

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Tool is something a model may call: a sub-workflow, an HTTP request, a code
// snippet, an MCP tool. Tool providers implement it; the agent loop consumes
// it through a Toolset. Implementations must be safe for concurrent use.
type Tool interface {
	// Definition describes the tool to the model. Name must be unique within
	// a Toolset and match [a-zA-Z0-9_-]{1,64} (the common provider limit).
	Definition() ToolDefinition
	// Call runs the tool with the model's JSON arguments. A tool that ran but
	// failed in a way the model can react to returns ToolResult{IsError: true}
	// and a nil error; a non-nil error means the tool could not run at all.
	Call(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// ToolResult is what a tool returns to the model.
type ToolResult struct {
	// Content is the text (often JSON) fed back as a tool message.
	Content string
	// IsError marks Content as an error description.
	IsError bool
}

// ErrDuplicateTool is returned when a Toolset gets two tools with one name.
var ErrDuplicateTool = errors.New("duplicate tool name")

// ErrUnknownTool is returned when a tool name is not in the Toolset.
var ErrUnknownTool = errors.New("unknown tool")

// Toolset is an ordered, name-unique collection of tools.
type Toolset struct {
	tools []Tool
	byKey map[string]Tool
}

// NewToolset builds a Toolset, rejecting empty and duplicate names.
func NewToolset(tools ...Tool) (*Toolset, error) {
	ts := &Toolset{byKey: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		name := t.Definition().Name
		if name == "" {
			return nil, errors.New("tool with empty name")
		}
		if _, dup := ts.byKey[name]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateTool, name)
		}
		ts.byKey[name] = t
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

// Lookup returns the tool with the given name.
func (ts *Toolset) Lookup(name string) (Tool, bool) {
	if ts == nil {
		return nil, false
	}
	t, ok := ts.byKey[name]
	return t, ok
}

// Definitions lists the tool definitions in insertion order.
func (ts *Toolset) Definitions() []ToolDefinition {
	if ts == nil {
		return nil
	}
	out := make([]ToolDefinition, len(ts.tools))
	for i, t := range ts.tools {
		out[i] = t.Definition()
	}
	return out
}

// Names lists the tool names sorted alphabetically.
func (ts *Toolset) Names() []string {
	if ts == nil {
		return nil
	}
	out := make([]string, 0, len(ts.byKey))
	for n := range ts.byKey {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Call runs the named tool; an unknown name returns ErrUnknownTool.
func (ts *Toolset) Call(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	t, ok := ts.Lookup(name)
	if !ok {
		return ToolResult{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	return t.Call(ctx, args)
}

// ToolProvider is implemented by a parent node's collaborator (Job 2) that
// supplies tools for one run, for example a tool sub-node connected to an
// agent. It exists so the agent depends on this contract, not on the nodes.
type ToolProvider interface {
	// Tools returns the tools the provider exposes for the current run.
	Tools(ctx context.Context) ([]Tool, error)
}

// ModelProvider supplies the chat model of a parent node (the ai.model
// sub-port): the connected model sub-node resolves its credential and returns
// the LLM plus the model settings the parent should use.
type ModelProvider interface {
	// Model returns the LLM and a request template (model name, temperature,
	// max tokens...) the parent merges its own messages and tools into.
	Model(ctx context.Context) (LLM, LLMRequest, error)
}
