package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
)

// previewScanLimit is how many recent executions are searched for run data of
// the node when the request names no execution.
const previewScanLimit = 20

type previewRequest struct {
	Expression string `json:"expression"`
	// Workflow is an optional unsaved document; it only supplies $workflow.
	Workflow json.RawMessage `json:"workflow"`
	// WorkflowID picks the workflow whose latest run is used when ExecutionID
	// is empty. It defaults to the id inside Workflow.
	WorkflowID  domain.WorkflowID  `json:"workflowId"`
	ExecutionID domain.ExecutionID `json:"executionId"`
	Node        string             `json:"node"`
	ItemIndex   int                `json:"itemIndex"`
	RunIndex    int                `json:"runIndex"`
}

type previewDTO struct {
	Value any `json:"value"`
}

// previewExpression evaluates an expression against the recorded input of a
// node in a past execution. An expression error answers 422.
func (a *API) previewExpression(w http.ResponseWriter, r *http.Request) error {
	if a.d.Expressions == nil {
		return fmt.Errorf("expression preview: %w", ErrNotImplemented)
	}
	var in previewRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	if in.Node == "" {
		return &domain.ValidationError{Issues: []domain.Issue{{Path: "node", Message: "is required"}}}
	}
	var wf domain.Workflow
	if t := bytes.TrimSpace(in.Workflow); len(t) > 0 && string(t) != "null" {
		d, err := jsoncodec.DecodeWorkflow(in.Workflow)
		if err != nil {
			return badRequest("invalid workflow document: %v", err)
		}
		wf = d
		if in.WorkflowID == "" {
			in.WorkflowID = d.ID
		}
	}
	ex, err := a.previewExecution(r, in)
	if err != nil {
		return err
	}
	if wf.ID == "" {
		wf.ID = ex.WorkflowID
	}
	v, err := a.d.Expressions.PreviewExpression(r.Context(), engine.Preview{
		Workflow: wf, ExecutionID: ex.ID, Mode: ex.Mode, RunData: ex.RunData,
		Node: in.Node, RunIndex: in.RunIndex, ItemIndex: in.ItemIndex, Expression: in.Expression,
	})
	if err != nil {
		return fmt.Errorf("previewing expression: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, previewDTO{Value: v})
}

// previewExecution loads the execution the preview reads: the named one, or
// the newest of the workflow that recorded a run of the node.
func (a *API) previewExecution(r *http.Request, in previewRequest) (domain.Execution, error) {
	ctx := r.Context()
	if in.ExecutionID != "" {
		ex, err := a.d.Executions.Get(ctx, in.ExecutionID)
		if err != nil {
			return domain.Execution{}, fmt.Errorf("loading execution: %w", err)
		}
		return ex, nil
	}
	if in.WorkflowID == "" {
		return domain.Execution{}, &domain.ValidationError{Issues: []domain.Issue{{Path: "executionId", Message: "give executionId, or workflowId (or a workflow with an id) to use its latest run"}}}
	}
	res, err := a.d.Executions.List(ctx, port.ExecutionFilter{WorkflowID: in.WorkflowID}, port.Page{Limit: previewScanLimit})
	if err != nil {
		return domain.Execution{}, fmt.Errorf("listing executions: %w", err)
	}
	for _, e := range res.Items {
		ex, err := a.d.Executions.Get(ctx, e.ID)
		if err != nil {
			return domain.Execution{}, fmt.Errorf("loading execution: %w", err)
		}
		if len(ex.RunData[in.Node]) > 0 {
			return ex, nil
		}
	}
	return domain.Execution{}, fmt.Errorf("no recent execution of workflow %s ran node %q: %w", in.WorkflowID, in.Node, domain.ErrNotFound)
}
