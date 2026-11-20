package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

var (
	_ nodesdk.Input          = (*nodeInput)(nil)
	_ nodesdk.ResumeSecreter = (*nodeInput)(nil)
	_ nodekit.RawParamSource = (*nodeInput)(nil)
)

// nodeInput implements nodesdk.Input for one attempt of one node run. It does
// not hold a context; every blocking method receives one from the caller.
type nodeInput struct {
	r        *runner
	info     *nodeInfo
	task     Task
	inputs   [][]domain.Item // what the node sees (after executeOnce)
	items    [][]nodesdk.Item
	runIndex int
	attempt  int
	log      *slog.Logger
	// bound pins every Param and EvalExpression to the parent's item boundItem
	// (set for sub-nodes, which have no items of their own).
	bound     bool
	boundItem int
}

var _ nodesdk.Input = (*nodeInput)(nil)

func newNodeInput(r *runner, info *nodeInfo, task Task, inputs [][]domain.Item, runIndex, attempt int) *nodeInput {
	items := make([][]nodesdk.Item, len(inputs))
	for i, p := range inputs {
		items[i] = toSDKItems(stampLineage(p, i))
	}
	return &nodeInput{
		r: r, info: info, task: task, inputs: inputs, items: items, runIndex: runIndex, attempt: attempt,
		log: r.e.d.Logger.With("node", info.node.Name, "runIndex", runIndex),
	}
}

func (n *nodeInput) Items(portIndex int) []nodesdk.Item {
	if portIndex < 0 || portIndex >= len(n.items) {
		return nil
	}
	return n.items[portIndex]
}

func (n *nodeInput) Param(ctx context.Context, name string, itemIndex int) (nodesdk.Value, error) {
	itemIndex = n.itemFor(itemIndex)
	raw, ok := n.info.node.Parameters[name]
	if !ok {
		prop, declared := findProperty(n.info.desc, name)
		switch {
		case declared && prop.Default != nil:
			raw = prop.Default
		case declared && prop.Required:
			return nil, fmt.Errorf("node %q parameter %q: %w", n.info.node.Name, name, nodesdk.ErrParamMissing)
		default:
			return nil, nil
		}
	}
	v, err := n.r.e.d.Evaluator.Resolve(ctx, raw, n.scope(itemIndex))
	if err != nil {
		var xe *domain.ExpressionError
		if errors.As(err, &xe) {
			xe.Node, xe.Parameter, xe.ItemIndex = n.info.node.Name, name, itemIndex
		}
		return nil, fmt.Errorf("resolving parameter %q: %w", name, err)
	}
	return v, nil
}

func (n *nodeInput) EvalExpression(ctx context.Context, expression string, itemIndex int) (nodesdk.Value, error) {
	itemIndex = n.itemFor(itemIndex)
	// The newline keeps a trailing line comment in the expression from
	// swallowing the closing delimiter.
	v, err := n.r.e.d.Evaluator.Resolve(ctx, "={{ "+expression+"\n}}", n.scope(itemIndex))
	if err != nil {
		return nil, fmt.Errorf("evaluating expression: %w", err)
	}
	return v, nil
}

func (n *nodeInput) itemFor(itemIndex int) int {
	if n.bound {
		return n.boundItem
	}
	return itemIndex
}

func findProperty(d nodesdk.Descriptor, name string) (nodesdk.Property, bool) {
	for _, p := range d.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return nodesdk.Property{}, false
}

func (n *nodeInput) scope(itemIndex int) port.ExpressionScope {
	var in []domain.Item
	if len(n.inputs) > 0 {
		in = n.inputs[0]
	}
	cur := domain.Item{JSON: map[string]any{}}
	if itemIndex >= 0 && itemIndex < len(in) {
		cur = in[itemIndex]
	}
	req := n.r.req
	return port.ExpressionScope{
		Item: cur, Input: in, ItemIndex: itemIndex, RunIndex: n.runIndex,
		Nodes:     &lineage{st: n.r.st, node: n.info.node.Name, runIndex: n.runIndex, sources: n.task.Sources, itemIndex: itemIndex},
		Vars:      req.Vars,
		Execution: port.ExecutionMeta{ID: n.r.st.ExecutionID, Mode: req.Mode, ResumeURL: resumeURL(n.r.e.d.ResumeBase, n.r.st)},
		Workflow:  port.WorkflowMeta{ID: req.Workflow.ID, Name: req.Workflow.Name},
	}
}

// ResumeSecret implements nodesdk.ResumeSecreter.
func (n *nodeInput) ResumeSecret() string { return n.r.st.ResumeSecret }

// RawParam implements nodekit.RawParamSource: the parameter as stored, before
// expressions are resolved.
func (n *nodeInput) RawParam(name string, _ int) (nodesdk.Value, bool) {
	if raw, ok := n.info.node.Parameters[name]; ok {
		return raw, true
	}
	if prop, declared := findProperty(n.info.desc, name); declared && prop.Default != nil {
		return prop.Default, true
	}
	return nil, false
}

func (n *nodeInput) Credential(ctx context.Context, name string) (nodesdk.Credential, error) {
	id, ok := n.info.node.Credentials[name]
	if !ok {
		return nodesdk.Credential{}, fmt.Errorf("node %q has no credential %q: %w", n.info.node.Name, name, domain.ErrNotFound)
	}
	c, err := n.r.e.d.Credentials.Resolve(ctx, id)
	if err != nil {
		return nodesdk.Credential{}, fmt.Errorf("resolving credential %q: %w", name, err)
	}
	return nodesdk.Credential(c), nil
}

func (n *nodeInput) Blob() nodesdk.BlobAccess {
	if b := n.r.e.d.Blob; b != nil {
		return b.Scoped(n.r.st.ExecutionID)
	}
	return nodesdk.UnavailableBlob{}
}

func (n *nodeInput) HTTP() nodesdk.HTTPClient {
	if h := n.r.e.d.HTTP; h != nil {
		return h
	}
	return nodesdk.UnavailableHTTP{}
}

func (n *nodeInput) Logger() *slog.Logger { return n.log }

func (n *nodeInput) ExecutionInfo() nodesdk.ExecutionInfo {
	req := n.r.req
	return nodesdk.ExecutionInfo{
		ExecutionID: string(n.r.st.ExecutionID), WorkflowID: string(req.Workflow.ID),
		WorkflowName: req.Workflow.Name, Mode: string(req.Mode), NodeName: n.info.node.Name,
		RunIndex: n.runIndex, Attempt: n.attempt, Depth: req.Depth,
	}
}
