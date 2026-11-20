package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
)

func workflowID(r *http.Request) (domain.WorkflowID, error) {
	return idParam(r, "id", domain.WorkflowID.Validate)
}

// readWorkflow reads a workflow document from the request body.
func readWorkflow(r *http.Request) (domain.Workflow, error) {
	body, err := readBody(r)
	if err != nil {
		return domain.Workflow{}, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return domain.Workflow{}, badRequest("the request body is empty")
	}
	if err := requireJSON(r); err != nil {
		return domain.Workflow{}, err
	}
	wf, err := jsoncodec.DecodeWorkflow(body)
	if err != nil {
		return domain.Workflow{}, badRequest("invalid workflow document: %v", err)
	}
	return wf, nil
}

// writeWorkflow answers with the workflow document and its ETag.
func (a *API) writeWorkflow(w http.ResponseWriter, r *http.Request, status int, wf domain.Workflow) error {
	doc, err := workflowDoc(wf)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(wf.Version)+`"`)
	return a.writeJSON(w, r, status, doc)
}

func (a *API) listWorkflows(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor", "active", "tag", "q"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	active, err := boolParam(q, "active")
	if err != nil {
		return err
	}
	res, err := a.d.Workflows.List(r.Context(), port.WorkflowFilter{Active: active, Tag: q.Get("tag"), NameContains: q.Get("q")}, page)
	if err != nil {
		return fmt.Errorf("listing workflows: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toWorkflowSummary))
}

func (a *API) createWorkflow(w http.ResponseWriter, r *http.Request) error {
	return a.storeNew(w, r, a.d.Workflows.Create)
}

// importWorkflow stores an exported document as a new workflow; the ID inside
// the document is ignored, so an import never overwrites anything.
func (a *API) importWorkflow(w http.ResponseWriter, r *http.Request) error {
	return a.storeNew(w, r, a.d.Workflows.Import)
}

func (a *API) storeNew(w http.ResponseWriter, r *http.Request, store func(ctx context.Context, wf domain.Workflow) (domain.Workflow, error)) error {
	wf, err := readWorkflow(r)
	if err != nil {
		return err
	}
	saved, err := store(r.Context(), wf)
	if err != nil {
		return fmt.Errorf("storing workflow: %w", err)
	}
	w.Header().Set("Location", v1("/workflows/"+string(saved.ID)))
	return a.writeWorkflow(w, r, http.StatusCreated, saved)
}

func (a *API) getWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	wf, err := a.d.Workflows.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("getting workflow: %w", err)
	}
	return a.writeWorkflow(w, r, http.StatusOK, wf)
}

// ifMatchVersion reads the workflow version from If-Match (`"3"`, `W/"3"` or
// `3`). Absent means no concurrency check.
func ifMatchVersion(r *http.Request) (int, error) {
	h := strings.TrimSpace(r.Header.Get("If-Match"))
	if h == "" {
		return 0, nil
	}
	h = strings.Trim(strings.TrimPrefix(h, "W/"), `"`)
	n, err := strconv.Atoi(h)
	if err != nil || n < 1 {
		return 0, badRequest("If-Match must be a workflow version such as \"3\"")
	}
	return n, nil
}

func (a *API) updateWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	expected, err := ifMatchVersion(r)
	if err != nil {
		return err
	}
	wf, err := readWorkflow(r)
	if err != nil {
		return err
	}
	saved, err := a.d.Workflows.Update(r.Context(), id, wf, expected)
	if err != nil {
		return fmt.Errorf("updating workflow: %w", err)
	}
	return a.writeWorkflow(w, r, http.StatusOK, saved)
}

func (a *API) deleteWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	if err := a.d.Workflows.Delete(r.Context(), id); err != nil {
		return fmt.Errorf("deleting workflow: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// exportWorkflow returns the document as a download. It is the same document
// the import route accepts.
func (a *API) exportWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	wf, err := a.d.Workflows.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("exporting workflow: %w", err)
	}
	doc, err := workflowDoc(wf)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", string(wf.ID)+".json"))
	return a.writeJSON(w, r, http.StatusOK, doc)
}

func (a *API) listVersions(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Workflows.ListVersions(r.Context(), id, page)
	if err != nil {
		return fmt.Errorf("listing versions: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, func(v domain.WorkflowVersion) versionSummaryDTO {
		return versionSummaryDTO{Version: v.Version, Name: v.Workflow.Name, CreatedAt: v.CreatedAt}
	}))
}

func (a *API) getVersion(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	n, err := pathInt(r, "version")
	if err != nil {
		return err
	}
	v, err := a.d.Workflows.GetVersion(r.Context(), id, n)
	if err != nil {
		return fmt.Errorf("getting version: %w", err)
	}
	doc, err := workflowDoc(v.Workflow)
	if err != nil {
		return err
	}
	return a.writeJSON(w, r, http.StatusOK, versionDTO{Version: v.Version, CreatedAt: v.CreatedAt, Workflow: doc})
}

func (a *API) restoreVersion(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	n, err := pathInt(r, "version")
	if err != nil {
		return err
	}
	wf, err := a.d.Workflows.Restore(r.Context(), id, n)
	if err != nil {
		return fmt.Errorf("restoring version: %w", err)
	}
	return a.writeWorkflow(w, r, http.StatusOK, wf)
}

func (a *API) activateWorkflow(w http.ResponseWriter, r *http.Request) error {
	return a.setActive(w, r, a.d.Activator.Activate)
}

func (a *API) deactivateWorkflow(w http.ResponseWriter, r *http.Request) error {
	return a.setActive(w, r, a.d.Activator.Deactivate)
}

func (a *API) setActive(w http.ResponseWriter, r *http.Request, change func(ctx context.Context, id domain.WorkflowID) error) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	if err := change(r.Context(), id); err != nil {
		return fmt.Errorf("changing workflow activation: %w", err)
	}
	wf, err := a.d.Workflows.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("reading workflow: %w", err)
	}
	return a.writeWorkflow(w, r, http.StatusOK, wf)
}

type testWebhookRequest struct {
	// Node names the webhook trigger node; optional when there is exactly one.
	Node string `json:"node"`
}

type testWebhookDTO struct {
	Method    string    `json:"method"`
	URLPath   string    `json:"urlPath"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// armTestWebhook makes the workflow's webhook node answer one call on its test
// URL; the editor then asks the user to trigger it.
func (a *API) armTestWebhook(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	var in testWebhookRequest
	if err := decodeJSON(r, &in, true); err != nil {
		return err
	}
	t, err := a.d.TestArmer.ArmTest(r.Context(), id, in.Node)
	if err != nil {
		return fmt.Errorf("arming test webhook: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, testWebhookDTO(t))
}

type runRequest struct {
	// StartNode names the node to begin at; empty picks the workflow's trigger.
	StartNode string `json:"startNode"`
	// Items feed the start node; each element is a JSON object.
	Items []map[string]any `json:"items"`
	// Destination makes the run partial: only the nodes needed to compute
	// destination.node run. Mode "inclusive" (default) runs the node too,
	// "exclusive" stops before it.
	Destination *runDestination `json:"destination"`
	// PinData replaces the output of the named nodes for this run only.
	PinData map[string][]map[string]any `json:"pinData"`
	// Workflow is an unsaved workflow document to run instead of the saved one.
	Workflow json.RawMessage `json:"workflow"`
}

type runDestination struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

const (
	destinationInclusive = "inclusive"
	destinationExclusive = "exclusive"
)

type startedDTO struct {
	ExecutionID domain.ExecutionID `json:"executionId"`
}

// runWorkflow starts a manual execution and answers 202; follow it through
// GET /executions/{id} or its event stream.
func (a *API) runWorkflow(w http.ResponseWriter, r *http.Request) error {
	id, err := workflowID(r)
	if err != nil {
		return err
	}
	var in runRequest
	if err := decodeJSON(r, &in, true); err != nil {
		return err
	}
	wf, err := a.d.Workflows.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("running workflow: %w", err)
	}
	var draft *domain.Workflow
	if len(bytes.TrimSpace(in.Workflow)) > 0 && string(bytes.TrimSpace(in.Workflow)) != "null" {
		d, err := jsoncodec.DecodeWorkflow(in.Workflow)
		if err != nil {
			return badRequest("invalid workflow document: %v", err)
		}
		d.ID, d.Active, d.Version = id, wf.Active, wf.Version
		if err := a.d.Workflows.Check(d); err != nil {
			return fmt.Errorf("validating workflow: %w", err)
		}
		draft, wf = &d, d
	}
	var dest string
	var exclusive bool
	if in.Destination != nil {
		dest = in.Destination.Node
		switch in.Destination.Mode {
		case "", destinationInclusive:
		case destinationExclusive:
			exclusive = true
		default:
			return &domain.ValidationError{Issues: []domain.Issue{{Path: "destination.mode", Message: "must be inclusive or exclusive"}}}
		}
	}
	pins := make(map[string][]domain.Item, len(in.PinData))
	for node, objs := range in.PinData {
		if _, ok := wf.NodeByName(node); !ok {
			return &domain.ValidationError{Issues: []domain.Issue{{Path: "pinData." + node, Message: fmt.Sprintf("workflow has no node %q", node)}}}
		}
		pins[node] = jsonItems(objs)
	}
	start := in.StartNode
	if start == "" {
		if start, err = a.d.StartNodes.FindStartNode(wf); err != nil {
			return &domain.ValidationError{Issues: []domain.Issue{{Path: "startNode", Message: err.Error()}}}
		}
	} else if _, ok := wf.NodeByName(start); !ok {
		return &domain.ValidationError{Issues: []domain.Issue{{Path: "startNode", Message: fmt.Sprintf("workflow has no node %q", start)}}}
	}
	execID, err := a.d.Executions.Start(r.Context(), execution.StartRequest{
		WorkflowID: id, Mode: domain.ModeManual, StartNode: start, Items: jsonItems(in.Items),
		Destination: dest, DestinationExclusive: exclusive, PinData: pins, Workflow: draft,
	})
	if err != nil {
		return fmt.Errorf("starting execution: %w", err)
	}
	w.Header().Set("Location", v1("/executions/"+string(execID)))
	return a.writeJSON(w, r, http.StatusAccepted, startedDTO{ExecutionID: execID})
}

// jsonItems wraps JSON objects as items.
func jsonItems(objs []map[string]any) []domain.Item {
	items := make([]domain.Item, len(objs))
	for i, o := range objs {
		items[i] = domain.Item{JSON: o}
	}
	return items
}
