// Package aitoolcode implements the Code tool sub-node (engi.ai.tool.code): a
// tool whose body is JavaScript run in the Code node's sandbox. The model's
// arguments arrive as the object "args"; the returned value is the tool result.
package aitoolcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/engi-hq/engi/internal/node/aikit"
	"github.com/engi-hq/engi/internal/node/code"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.tool.code"

const defaultCode = "// args holds the arguments the model passed. Return the result.\nreturn { echo: args.input };"

// Config is what the node needs from the host.
type Config struct {
	// Limits bound one run (the nodes.code settings).
	Limits code.Limits
}

// Node is the Code tool sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns a Code tool node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	props := aikit.ToolProperties("run_code", "Runs a small JavaScript function and returns its result.")
	props = append(props,
		aikit.SchemaProperty("JSON Schema of the arguments the model fills in; they are available to the code as args."),
		nodesdk.Property{Name: "jsCode", DisplayName: "JavaScript", Type: nodesdk.PropCode, Default: defaultCode, Required: true,
			Description: "Function body. In scope: args (the model's arguments), $fn helpers and console.log. Return a JSON value; a string is returned as is."})
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Code Tool", Group: nodesdk.GroupAI, Icon: "code",
		Description: "A tool that lets an agent run your JavaScript in the sandbox of the Code node.",
		Outputs:     []nodesdk.PortSpec{{Name: "tool", Kind: nodesdk.PortAITool, DisplayName: "Tool"}},
		Properties:  props,
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("code tool: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ToolProvider.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	meta, err := aikit.ReadToolMeta(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // descriptive
	}
	src, err := nodesdk.ParamString(ctx, in, "jsCode", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // SDK errors are descriptive
	}
	snip, err := code.NewSnippet(src)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", meta.Def.Name, err)
	}
	return aikit.Tools{&tool{meta: meta, snip: snip, limits: n.cfg.Limits, log: in.Logger()}}, nil
}

type tool struct {
	meta   aikit.ToolMeta
	snip   *code.Snippet
	limits code.Limits
	log    *slog.Logger
}

// Definition implements port.Tool.
func (t *tool) Definition() port.ToolDefinition { return t.meta.Def }

// Call implements port.Tool.
func (t *tool) Call(ctx context.Context, raw json.RawMessage) (port.ToolResult, error) {
	args, err := t.meta.Schema.ParseArgs(raw)
	if err != nil {
		return aikit.ErrorResult("%v", err), nil
	}
	res, err := t.snip.Run(ctx, t.limits, args, t.log)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return port.ToolResult{}, fmt.Errorf("code tool canceled: %w", err)
		}
		return aikit.ErrorResult("%v", err), nil
	}
	switch v := res.(type) {
	case nil:
		return port.ToolResult{Content: "null"}, nil
	case string:
		return port.ToolResult{Content: v}, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return aikit.ErrorResult("the code returned data that cannot be serialized: %v", err), nil
	}
	return port.ToolResult{Content: string(b)}, nil
}
