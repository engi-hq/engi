// Package executeworkflow implements the Execute Workflow node, which runs
// another workflow with the node's input items, either waiting for its result
// or leaving it to run on its own.
package executeworkflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.executeWorkflow"

// Node is the Execute Workflow node.
type Node struct {
	runner   nodesdk.SubworkflowRunner
	maxDepth int
}

// New returns an Execute Workflow node that starts children through runner and
// refuses to nest deeper than maxDepth levels (engine.maxSubworkflowDepth).
func New(runner nodesdk.SubworkflowRunner, maxDepth int) nodesdk.Node {
	return Node{runner: runner, maxDepth: maxDepth}
}

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Execute Workflow", Group: nodesdk.GroupFlow, Icon: "workflow",
		Description: "Run another workflow with these items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "workflowId", DisplayName: "Workflow ID", Type: nodesdk.PropString, Required: true,
				Description: "The workflow to run. It must start with an Execute Workflow Trigger node."},
			{Name: "waitForResult", DisplayName: "Wait for result", Type: nodesdk.PropBoolean, Default: true,
				Description: "Continue with the output of the child's last node. When off, the child runs on its own and the input items continue unchanged."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	workflowID, err := nodesdk.ParamString(ctx, in, "workflowId", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading workflowId: %w", err)
	}
	wait, err := nodesdk.ParamBool(ctx, in, "waitForResult", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading waitForResult: %w", err)
	}
	info := in.ExecutionInfo()
	if info.Depth+1 > n.maxDepth {
		return nodesdk.Output{}, &nodesdk.NodeError{
			Message:     fmt.Sprintf("sub-workflow nesting would reach depth %d, the limit is %d", info.Depth+1, n.maxDepth),
			Description: "A workflow that calls itself, directly or through others, hits this limit. Raise engine.maxSubworkflowDepth if the nesting is intended.",
		}
	}
	items := in.Items(0)
	res, err := n.runner.RunSubworkflow(ctx, nodesdk.SubworkflowCall{
		WorkflowID: workflowID, ParentExecutionID: info.ExecutionID, Depth: info.Depth + 1, Items: items, WaitForResult: wait,
	})
	if err != nil {
		return nodesdk.Output{}, &nodesdk.NodeError{Message: "starting sub-workflow failed", Description: err.Error(), Err: err}
	}
	if !wait {
		out := make([]nodesdk.Item, len(items))
		for i, it := range items {
			out[i] = nodekit.Paired(it, i)
		}
		return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
	}
	if res.Status != "success" {
		msg := res.Error
		if msg == "" {
			msg = "the sub-workflow did not finish"
		}
		return nodesdk.Output{}, &nodesdk.NodeError{
			Message:     fmt.Sprintf("sub-workflow %s ended with status %q", res.ExecutionID, res.Status),
			Description: msg, Err: errors.New(msg),
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{res.Items}}, nil
}
