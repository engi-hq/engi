package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Defaults of the status the node leaves unset.
const (
	defaultImmediateStatus = http.StatusAccepted
	defaultResultStatus    = http.StatusOK
)

// answer starts the execution and replies in the node's response mode.
func (h *Handler) answer(w http.ResponseWriter, r *http.Request, route trigger.Route, req execution.StartRequest) {
	switch route.Spec.ResponseMode {
	case nodesdk.ResponseLastNode:
		h.answerLastNode(w, r, route, req)
	case nodesdk.ResponseNode:
		h.answerViaNode(w, r, route, req)
	default:
		h.answerImmediately(w, r, route, req)
	}
}

func (h *Handler) answerImmediately(w http.ResponseWriter, r *http.Request, route trigger.Route, req execution.StartRequest) {
	id, err := h.d.Executions.Start(r.Context(), req)
	if existing, dup := duplicateOf(err); dup {
		id, err = existing, nil
	}
	if err != nil {
		h.fail(w, "starting webhook execution failed", err)
		return
	}
	status := route.Spec.ResponseCode
	if status == 0 {
		status = defaultImmediateStatus
	}
	if body := route.Spec.ResponseBody; body != "" {
		ct := "text/plain; charset=utf-8"
		if json.Valid([]byte(body)) {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return
	}
	writeJSON(w, status, "application/json", map[string]any{"executionId": id})
}

// answerLastNode holds the call until the execution ends.
func (h *Handler) answerLastNode(w http.ResponseWriter, r *http.Request, route trigger.Route, req execution.StartRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.ResponseTimeout)
	defer cancel()
	ex, err := h.d.Executions.RunSync(ctx, req)
	if existing, dup := duplicateOf(err); dup {
		ex, err = h.d.Executions.Await(ctx, existing)
	}
	if err != nil {
		h.syncFailure(w, r, err)
		return
	}
	h.writeResult(w, route, ex)
}

// syncFailure answers when waiting for the execution failed.
func (h *Handler) syncFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
		// The caller went away; the execution carries on.
	case errors.Is(err, execution.ErrSyncTimeout), errors.Is(err, context.DeadlineExceeded):
		problem(w, http.StatusGatewayTimeout, "Workflow did not answer in time", "")
	default:
		h.fail(w, "running webhook execution failed", err)
	}
}

// writeResult answers with the last node's output.
func (h *Handler) writeResult(w http.ResponseWriter, route trigger.Route, ex domain.Execution) {
	switch ex.Status {
	case domain.StatusSuccess:
	case domain.StatusWaiting:
		writeJSON(w, defaultImmediateStatus, "application/json", map[string]any{"executionId": ex.ID, "status": ex.Status})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, "application/problem+json", map[string]any{
			"type": "about:blank", "title": "Workflow execution failed", "status": http.StatusInternalServerError, "executionId": ex.ID,
		})
		return
	}
	status := route.Spec.ResponseCode
	if status == 0 {
		status = defaultResultStatus
	}
	items := ex.LastOutput()
	switch route.Spec.ResponseData {
	case "noData":
		w.WriteHeader(status)
	case "allEntries":
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = it.JSON
		}
		writeJSON(w, status, "application/json", out)
	default:
		var first any = map[string]any{}
		if len(items) > 0 {
			first = items[0].JSON
		}
		writeJSON(w, status, "application/json", first)
	}
}

// answerViaNode holds the call until a Respond to Webhook node produces the
// answer. The node's run is published on the bus, so the process holding the
// connection need not be the one running the workflow.
func (h *Handler) answerViaNode(w http.ResponseWriter, r *http.Request, route trigger.Route, req execution.StartRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.ResponseTimeout)
	defer cancel()
	// Subscribe before starting: the execution id is unknown yet, so listen to
	// every execution's events and pick ours afterwards.
	sub, err := h.d.Bus.Subscribe(ctx, port.EventFilter{Kinds: []port.EventKind{port.EventNodeFinished, port.EventExecutionFinished}})
	if err != nil {
		h.fail(w, "subscribing for the webhook response failed", err)
		return
	}
	defer sub.Close()
	id, err := h.d.Executions.Start(ctx, req)
	if existing, dup := duplicateOf(err); dup {
		ex, err := h.d.Executions.Await(ctx, existing)
		if err != nil {
			h.syncFailure(w, r, err)
			return
		}
		h.writeResult(w, route, ex)
		return
	}
	if err != nil {
		h.fail(w, "starting webhook execution failed", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			h.syncFailure(w, r, ctx.Err())
			return
		case e, ok := <-sub.Events():
			if !ok {
				problem(w, http.StatusInternalServerError, "Internal error", "")
				return
			}
			if e.ExecutionID != id {
				continue
			}
			if e.Kind == port.EventNodeFinished && e.Run != nil && e.Run.Response != nil {
				writeResponse(w, e.Run.Response)
				return
			}
			if e.Kind == port.EventExecutionFinished {
				h.finishedWithoutResponse(w, id, e)
				return
			}
		}
	}
}

// finishedWithoutResponse answers when the execution ended before any
// Respond to Webhook node ran.
func (h *Handler) finishedWithoutResponse(w http.ResponseWriter, id domain.ExecutionID, e port.Event) {
	if e.Status == domain.StatusWaiting {
		writeJSON(w, defaultImmediateStatus, "application/json", map[string]any{"executionId": id, "status": e.Status})
		return
	}
	detail := "the workflow finished without reaching a Respond to Webhook node"
	if e.Status != domain.StatusSuccess {
		detail = "the workflow failed before responding"
	}
	writeJSON(w, http.StatusInternalServerError, "application/problem+json", map[string]any{
		"type": "about:blank", "title": "Workflow did not respond", "status": http.StatusInternalServerError,
		"detail": detail, "executionId": id,
	})
}

// writeResponse sends the reply a Respond to Webhook node produced.
func writeResponse(w http.ResponseWriter, resp *domain.WebhookResponse) {
	for k, vs := range resp.Headers {
		if hopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status == 0 {
		status = defaultResultStatus
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}

// hopByHop lists headers the server sets itself.
func hopByHop(name string) bool {
	switch strings.ToLower(name) {
	case "content-length", "transfer-encoding", "connection", "keep-alive", "upgrade", "te", "trailer":
		return true
	}
	return false
}
