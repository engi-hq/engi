package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Preview describes one expression evaluation against recorded run data.
type Preview struct {
	Workflow    domain.Workflow
	ExecutionID domain.ExecutionID
	Mode        domain.ExecutionMode
	// RunData is the recorded result of a past execution.
	RunData map[string][]domain.NodeRun
	// Node, RunIndex and ItemIndex pick the input item the expression sees.
	Node      string
	RunIndex  int
	ItemIndex int
	// ResumeSecret is the execution's resume secret, if the caller has it, so the
	// preview shows $execution.resumeUrl. Empty leaves the resume URL unset.
	ResumeSecret string
	// Expression is bare JavaScript, or a template when it starts with "=".
	Expression string
}

// PreviewExpression evaluates an expression as the node would have seen it in
// the recorded run, using the engine's evaluator and its configured limits.
// Failures are *domain.ExpressionError; a missing node or run wraps
// domain.ErrNotFound.
func (e *Engine) PreviewExpression(ctx context.Context, p Preview) (any, error) {
	var run *domain.NodeRun
	for i := range p.RunData[p.Node] {
		if p.RunData[p.Node][i].RunIndex == p.RunIndex {
			run = &p.RunData[p.Node][i]
			break
		}
	}
	if run == nil {
		return nil, fmt.Errorf("node %q has no run %d in the execution: %w", p.Node, p.RunIndex, domain.ErrNotFound)
	}
	var in []domain.Item
	if ports := run.Input[domain.PortMain]; len(ports) > 0 {
		in = ports[0]
	}
	cur := domain.Item{JSON: map[string]any{}}
	if p.ItemIndex >= 0 && p.ItemIndex < len(in) {
		cur = in[p.ItemIndex]
	}
	st := &State{ExecutionID: p.ExecutionID, RunData: p.RunData}
	scope := port.ExpressionScope{
		Item: cur, Input: in, ItemIndex: p.ItemIndex, RunIndex: p.RunIndex,
		Nodes:     &lineage{st: st, node: p.Node, runIndex: p.RunIndex, sources: run.Sources, itemIndex: p.ItemIndex},
		Execution: port.ExecutionMeta{ID: p.ExecutionID, Mode: p.Mode, ResumeURL: resumeURL(e.d.ResumeBase, &State{ExecutionID: p.ExecutionID, ResumeSecret: p.ResumeSecret})},
		Workflow:  port.WorkflowMeta{ID: p.Workflow.ID, Name: p.Workflow.Name},
	}
	tpl := p.Expression
	if !strings.HasPrefix(tpl, "=") {
		tpl = "={{ " + tpl + "\n}}" // the newline keeps a trailing line comment from eating the delimiter
	}
	v, err := e.d.Evaluator.Resolve(ctx, tpl, scope)
	if err != nil {
		return nil, fmt.Errorf("previewing expression: %w", err)
	}
	return v, nil
}
