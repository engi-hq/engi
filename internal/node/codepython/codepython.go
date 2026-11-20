// Package codepython implements the Python Code node: user Python run once for
// all items or once per item by a Runner.
//
// The node owns the parameters, the mode semantics and the shape of the
// result. How Python is started and sandboxed is the Runner's business (see
// internal/adapter/plugin/pyrunner), so this package has no process or
// protocol knowledge.
package codepython

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.code.python"

// Modes a Job runs in.
const (
	ModeAll  = "runOnceForAllItems"
	ModeEach = "runOnceForEachItem"
)

// defaultCode is the code a new node starts with.
const defaultCode = "# items is a list of {\"json\": ..., \"binary\": ...} dicts. Return a list of items.\nreturn items"

// Job is one Python run.
type Job struct {
	// Code is the function body written by the user.
	Code string
	// Mode is ModeAll or ModeEach.
	Mode string
	// Items are the input items of port 0.
	Items []nodesdk.Item
	// Execution describes the running execution.
	Execution nodesdk.ExecutionInfo
	// AllowedModules are the modules user code may import.
	AllowedModules []string
	// Timeout bounds the whole run (all items of the per-item mode included).
	Timeout time.Duration
	// MaxOutputBytes bounds the serialized result.
	MaxOutputBytes int
	// Eval evaluates an expression in the scope of the given item, as a node
	// parameter would be.
	Eval func(ctx context.Context, expression string, itemIndex int) (any, error)
	// Log receives what user code prints.
	Log func(message string)
}

// Result is what a Job produced.
type Result struct {
	// Items is the result of ModeAll.
	Items []nodesdk.Item
	// PerItem is the result of ModeEach: one entry per input item, holding the
	// items it produced. A nil entry means the item was dropped.
	PerItem [][]nodesdk.Item
}

// Runner executes a Job. A failure in user code is a *nodesdk.NodeError; one
// that happened while processing an input item carries its ItemIndex.
type Runner interface {
	Run(ctx context.Context, job Job) (Result, error)
}

// Limits bound one run. They are copied into every Job.
type Limits struct {
	// Timeout bounds one run of user code.
	Timeout time.Duration
	// MaxOutputBytes bounds the serialized result.
	MaxOutputBytes int
	// AllowedModules lists the modules user code may import.
	AllowedModules []string
}

// Node is the Python Code node.
type Node struct {
	runner Runner
	limits Limits
}

// New returns a Python Code node that runs jobs on runner.
func New(runner Runner, limits Limits) nodesdk.Node { return Node{runner: runner, limits: limits} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Code (Python)", Group: nodesdk.GroupTransform, Icon: "code",
		Description: "Run Python in a separate process to transform items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: ModeAll, Options: []nodesdk.Option{
				{Name: "Run once for all items", Value: ModeAll, Description: "items holds every item; return a list of items (or plain dicts)."},
				{Name: "Run once for each item", Value: ModeEach, Description: "json is the current item; return a dict, a list of dicts, or None to drop it."}}},
			{Name: "pythonCode", DisplayName: "Python", Type: nodesdk.PropCode, Default: defaultCode, Required: true,
				Description: "Function body. In scope: items, item, json, item_index, run_index, execution, workflow, expr(expression), print."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading mode: %w", err)
	}
	if mode == "" {
		mode = ModeAll
	}
	if mode != ModeAll && mode != ModeEach {
		return nodesdk.Output{}, fmt.Errorf("unknown mode %q", mode)
	}
	src, err := nodesdk.ParamString(ctx, in, "pythonCode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading pythonCode: %w", err)
	}
	items := in.Items(0)
	job := Job{
		Code: src, Mode: mode, Items: items, Execution: in.ExecutionInfo(),
		AllowedModules: n.limits.AllowedModules, Timeout: n.limits.Timeout, MaxOutputBytes: n.limits.MaxOutputBytes,
		Eval: func(ctx context.Context, expression string, itemIndex int) (any, error) {
			return in.EvalExpression(ctx, expression, itemIndex)
		},
		Log: func(message string) {
			in.Logger().Info("code output", "language", "python", "message", message)
		},
	}
	res, err := n.runner.Run(ctx, job)
	if err != nil {
		return nodesdk.Output{}, err
	}
	if mode == ModeAll {
		return nodesdk.Output{Ports: [][]nodesdk.Item{res.Items}}, nil
	}
	if len(res.PerItem) != len(items) {
		return nodesdk.Output{}, errors.New("python runner returned a result for a different number of items")
	}
	var out []nodesdk.Item
	for i, produced := range res.PerItem {
		for _, p := range produced {
			if len(p.PairedItem) == 0 {
				p.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
			}
			out = append(out, p)
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
