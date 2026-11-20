// Package aitoolworkflow implements the Workflow tool sub-node
// (engi.ai.tool.workflow): a tool that runs another workflow. The model's
// arguments become the JSON of one input item of the child's Execute Workflow
// Trigger; the child's last-node output is the tool result.
package aitoolworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/node/aikit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.tool.workflow"

// Config is what the node needs from the host.
type Config struct {
	// Runner starts the child execution. Nil makes Supply fail with
	// nodesdk.ErrUnavailable.
	Runner nodesdk.SubworkflowRunner
	// MaxDepth is the nesting limit (engine.maxSubworkflowDepth).
	MaxDepth int
	// Timeout bounds one run of the child.
	Timeout time.Duration
}

// Node is the Workflow tool sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns a Workflow tool node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	props := aikit.ToolProperties("run_workflow", "Runs a workflow and returns its result.")
	props = append(props,
		nodesdk.Property{Name: "workflowId", DisplayName: "Workflow ID", Type: nodesdk.PropString, Required: true,
			Description: "The workflow to run. It must start with an Execute Workflow Trigger node."},
		aikit.SchemaProperty("JSON Schema of the arguments the model fills in. They become the JSON of the child's input item."))
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Workflow Tool", Group: nodesdk.GroupAI, Icon: "workflow",
		Description: "A tool that lets an agent run another workflow and read its result.",
		Outputs:     []nodesdk.PortSpec{{Name: "tool", Kind: nodesdk.PortAITool, DisplayName: "Tool"}},
		Properties:  props,
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("workflow tool: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ToolProvider.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	if n.cfg.Runner == nil {
		return nil, fmt.Errorf("sub-workflows: %w", nodesdk.ErrUnavailable)
	}
	meta, err := aikit.ReadToolMeta(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // descriptive
	}
	id, err := nodesdk.ParamString(ctx, in, "workflowId", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if id = strings.TrimSpace(id); id == "" {
		return nil, errors.New("workflow ID is required")
	}
	info := in.ExecutionInfo()
	if info.Depth+1 > n.cfg.MaxDepth {
		return nil, &nodesdk.NodeError{
			Message:     fmt.Sprintf("sub-workflow nesting would reach depth %d, the limit is %d", info.Depth+1, n.cfg.MaxDepth),
			Description: "Raise engine.maxSubworkflowDepth if the nesting is intended.",
		}
	}
	return aikit.Tools{&tool{cfg: n.cfg, meta: meta, workflowID: id, parent: info.ExecutionID, depth: info.Depth + 1}}, nil
}

type tool struct {
	cfg        Config
	meta       aikit.ToolMeta
	workflowID string
	parent     string
	depth      int
}

// Definition implements port.Tool.
func (t *tool) Definition() port.ToolDefinition { return t.meta.Def }

// Call implements port.Tool.
func (t *tool) Call(ctx context.Context, raw json.RawMessage) (port.ToolResult, error) {
	args, err := t.meta.Schema.ParseArgs(raw)
	if err != nil {
		return aikit.ErrorResult("%v", err), nil
	}
	if t.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.cfg.Timeout)
		defer cancel()
	}
	res, err := t.cfg.Runner.RunSubworkflow(ctx, nodesdk.SubworkflowCall{
		WorkflowID: t.workflowID, ParentExecutionID: t.parent, Depth: t.depth,
		Items: []nodesdk.Item{{JSON: args}}, WaitForResult: true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return aikit.ErrorResult("the workflow did not finish in time: %v", ctx.Err()), nil
		}
		return aikit.ErrorResult("starting the workflow failed: %v", err), nil
	}
	if res.Status != "success" {
		msg := res.Error
		if msg == "" {
			msg = "the workflow did not finish"
		}
		return aikit.ErrorResult("workflow ended with status %q: %s", res.Status, msg), nil
	}
	return result(res.Items)
}

// result encodes the child's output: one item is its JSON object, several are
// an array, none is an empty array.
func result(items []nodesdk.Item) (port.ToolResult, error) {
	var v any
	switch len(items) {
	case 1:
		v = items[0].JSON
	default:
		list := make([]any, len(items))
		for i, it := range items {
			list[i] = it.JSON
		}
		v = list
	}
	b, err := json.Marshal(v)
	if err != nil {
		return aikit.ErrorResult("the workflow result cannot be serialized: %v", err), nil
	}
	return port.ToolResult{Content: string(b)}, nil
}
