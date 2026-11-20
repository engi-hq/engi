package execution

import (
	"context"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// InlineRunner runs an already stored execution in the calling process,
// without going through the queue.
type InlineRunner interface {
	RunInline(ctx context.Context, id domain.ExecutionID) error
}

// Subworkflows implements nodesdk.SubworkflowRunner. A child the caller waits
// for runs inline, inside the parent's worker slot: queueing it could deadlock
// when every slot is held by a parent waiting for its child. A fire-and-forget
// child is queued like any other execution.
type Subworkflows struct {
	svc         *Service
	inline      InlineRunner
	triggerType string
}

var _ nodesdk.SubworkflowRunner = (*Subworkflows)(nil)

// NewSubworkflows builds the runner. triggerType is the node type that starts
// a sub-workflow (the Execute Workflow Trigger).
func NewSubworkflows(svc *Service, inline InlineRunner, triggerType string) (*Subworkflows, error) {
	if svc == nil || inline == nil || triggerType == "" {
		return nil, errors.New("subworkflow runner requires a service, an inline runner and a trigger node type")
	}
	return &Subworkflows{svc: svc, inline: inline, triggerType: triggerType}, nil
}

// RunSubworkflow implements nodesdk.SubworkflowRunner.
func (r *Subworkflows) RunSubworkflow(ctx context.Context, call nodesdk.SubworkflowCall) (nodesdk.SubworkflowResult, error) {
	wfID := domain.WorkflowID(call.WorkflowID)
	wf, err := r.svc.workflow(ctx, wfID, 0)
	if err != nil {
		return nodesdk.SubworkflowResult{}, err
	}
	start, err := r.startNode(wf)
	if err != nil {
		return nodesdk.SubworkflowResult{}, err
	}
	parent := domain.ExecutionID(call.ParentExecutionID)
	req := StartRequest{
		WorkflowID: wfID, Mode: domain.ModeSubworkflow, StartNode: start,
		Items: fromSDK(call.Items), ParentExecutionID: &parent,
	}
	if !call.WaitForResult {
		id, err := r.svc.Start(ctx, req)
		if err != nil {
			return nodesdk.SubworkflowResult{}, err
		}
		return nodesdk.SubworkflowResult{ExecutionID: string(id), Status: string(domain.StatusQueued)}, nil
	}
	id, err := r.svc.create(ctx, req, false)
	if err != nil {
		return nodesdk.SubworkflowResult{}, err
	}
	if err := r.inline.RunInline(ctx, id); err != nil {
		return nodesdk.SubworkflowResult{ExecutionID: string(id)}, fmt.Errorf("running sub-workflow %s: %w", id, err)
	}
	ex, err := r.svc.Get(ctx, id)
	if err != nil {
		return nodesdk.SubworkflowResult{}, err
	}
	res := nodesdk.SubworkflowResult{ExecutionID: string(id), Status: string(ex.Status), Items: toSDK(ex.LastOutput())}
	if ex.Error != nil {
		res.Error = ex.Error.Message
	}
	return res, nil
}

func (r *Subworkflows) startNode(wf domain.Workflow) (string, error) {
	for _, name := range wf.NodesInCanvasOrder() {
		if n, _ := wf.NodeByName(name); n.Type == r.triggerType && !n.Disabled {
			return name, nil
		}
	}
	return "", fmt.Errorf("workflow %s has no %s node, so it cannot be called as a sub-workflow: %w", wf.ID, r.triggerType, domain.ErrNotFound)
}

func fromSDK(items []nodesdk.Item) []domain.Item {
	out := make([]domain.Item, len(items))
	for i, it := range items {
		d := domain.Item{JSON: it.JSON}
		if len(it.Binary) > 0 {
			d.Binary = make(map[string]domain.BinaryRef, len(it.Binary))
			for k, b := range it.Binary {
				d.Binary[k] = domain.BinaryRef(b)
			}
		}
		out[i] = d
	}
	return out
}

func toSDK(items []domain.Item) []nodesdk.Item {
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		s := nodesdk.Item{JSON: it.JSON}
		if len(it.Binary) > 0 {
			s.Binary = make(map[string]nodesdk.BinaryRef, len(it.Binary))
			for k, b := range it.Binary {
				s.Binary[k] = nodesdk.BinaryRef(b)
			}
		}
		out[i] = s
	}
	return out
}
