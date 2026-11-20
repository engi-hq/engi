package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

func executionID(r *http.Request) (domain.ExecutionID, error) {
	return idParam(r, "id", domain.ExecutionID.Validate)
}

var knownStatuses = []domain.ExecutionStatus{
	domain.StatusQueued, domain.StatusRunning, domain.StatusWaiting, domain.StatusSuccess,
	domain.StatusError, domain.StatusCanceled, domain.StatusCrashed,
}

// statusParams reads status filters: repeated (status=a&status=b) or comma
// separated.
func statusParams(values []string) ([]domain.ExecutionStatus, error) {
	var out []domain.ExecutionStatus
	for _, v := range values {
		for s := range strings.SplitSeq(v, ",") {
			st := domain.ExecutionStatus(strings.TrimSpace(s))
			if !slices.Contains(knownStatuses, st) {
				return nil, badRequest("unknown status %q", s)
			}
			out = append(out, st)
		}
	}
	return out, nil
}

func (a *API) listExecutions(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor", "status", "workflowId", "from", "to"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	statuses, err := statusParams(q["status"])
	if err != nil {
		return err
	}
	from, err := timeParam(q, "from")
	if err != nil {
		return err
	}
	to, err := timeParam(q, "to")
	if err != nil {
		return err
	}
	f := port.ExecutionFilter{WorkflowID: domain.WorkflowID(q.Get("workflowId")), Statuses: statuses, StartedAfter: from, StartedBefore: to}
	res, err := a.d.Executions.List(r.Context(), f, page)
	if err != nil {
		return fmt.Errorf("listing executions: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toExecutionDTO))
}

func (a *API) getExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := executionID(r)
	if err != nil {
		return err
	}
	ex, err := a.d.Executions.Get(r.Context(), id)
	if err != nil {
		return fmt.Errorf("getting execution: %w", err)
	}
	dto, err := toExecutionDetail(ex)
	if err != nil {
		return err
	}
	return a.writeJSON(w, r, http.StatusOK, dto)
}

func (a *API) deleteExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := executionID(r)
	if err != nil {
		return err
	}
	if err := a.d.Executions.Delete(r.Context(), id); err != nil {
		return fmt.Errorf("deleting execution: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// cancelExecution answers 202: a running execution stops when its worker
// notices, so the final status arrives asynchronously.
func (a *API) cancelExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := executionID(r)
	if err != nil {
		return err
	}
	if err := a.d.Executions.Cancel(r.Context(), id); err != nil {
		return fmt.Errorf("canceling execution: %w", err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (a *API) retryExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := executionID(r)
	if err != nil {
		return err
	}
	newID, err := a.d.Executions.Retry(r.Context(), id)
	if err != nil {
		return fmt.Errorf("retrying execution: %w", err)
	}
	w.Header().Set("Location", v1("/executions/"+string(newID)))
	return a.writeJSON(w, r, http.StatusAccepted, startedDTO{ExecutionID: newID})
}
