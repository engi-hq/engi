package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// SSE event names (docs/09 section 9.5).
const (
	eventStarted      = "execution.started"
	eventNodeStarted  = "node.started"
	eventNodeFinished = "node.finished"
	eventWaiting      = "execution.waiting"
	eventFinished     = "execution.finished"
)

// streamEvent is the payload of every SSE event. Item payloads are never sent;
// clients fetch them from GET /executions/{id}.
type streamEvent struct {
	ExecutionID domain.ExecutionID     `json:"executionId"`
	WorkflowID  domain.WorkflowID      `json:"workflowId,omitempty"`
	At          time.Time              `json:"at"`
	Node        string                 `json:"node,omitempty"`
	RunIndex    *int                   `json:"runIndex,omitempty"`
	Status      domain.ExecutionStatus `json:"status,omitempty"`
	NodeStatus  domain.NodeRunStatus   `json:"nodeStatus,omitempty"`
	DurationMs  *int64                 `json:"durationMs,omitempty"`
	Attempts    *int                   `json:"attempts,omitempty"`
	OutputItems []int                  `json:"outputItems,omitempty"`
	WaitUntil   *time.Time             `json:"waitUntil,omitempty"`
	Error       *errorInfoDTO          `json:"error,omitempty"`
}

// streamKinds are the bus events the stream forwards.
var streamKinds = []port.EventKind{
	port.EventExecutionStarted, port.EventNodeStarted, port.EventNodeFinished, port.EventExecutionFinished,
}

// toStreamEvent maps a bus event to its SSE name and payload; ok is false for
// events the stream does not forward.
func toStreamEvent(e port.Event) (name string, p streamEvent, ok bool) {
	p = streamEvent{ExecutionID: e.ExecutionID, WorkflowID: e.WorkflowID, At: e.At}
	switch e.Kind {
	case port.EventExecutionStarted:
		return eventStarted, p, true
	case port.EventNodeStarted:
		idx := e.RunIndex
		p.Node, p.RunIndex = e.Node, &idx
		return eventNodeStarted, p, true
	case port.EventNodeFinished:
		idx := e.RunIndex
		p.Node, p.RunIndex = e.Node, &idx
		if e.Run != nil {
			ms, attempts := e.Run.Duration.Milliseconds(), e.Run.Attempts
			p.NodeStatus, p.DurationMs, p.Attempts = e.Run.Status, &ms, &attempts
			p.Error = toErrorInfoDTO(e.Run.Error)
			for _, items := range e.Run.Output[domain.PortMain] {
				p.OutputItems = append(p.OutputItems, len(items))
			}
		}
		return eventNodeFinished, p, true
	case port.EventExecutionFinished:
		p.Status, p.Error = e.Status, toErrorInfoDTO(e.Error)
		if e.Status == domain.StatusWaiting {
			return eventWaiting, p, true
		}
		return eventFinished, p, true
	}
	return "", p, false
}

// streamExecution serves GET /executions/{id}/stream as server-sent events.
//
// It subscribes before it reads the execution, so no event can slip between
// the two. An execution that is already over is answered with its final event
// and the stream closes; otherwise events flow until the execution reaches a
// terminal status or the client leaves. The bus is best effort and keeps no
// history, so Last-Event-ID cannot replay: a reconnect starts from the current
// state (documented in docs/09 section 9.5).
func (a *API) streamExecution(w http.ResponseWriter, r *http.Request) error {
	id, err := executionID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	sub, err := a.d.Bus.Subscribe(ctx, port.EventFilter{ExecutionID: id, Kinds: streamKinds})
	if err != nil {
		return fmt.Errorf("subscribing to execution events: %w", err)
	}
	defer sub.Close()
	ex, err := a.d.Executions.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("streaming execution: %w", err)
	}

	rc := http.NewResponseController(w)
	// The stream outlives the server's write timeout; each write is still
	// bounded by the heartbeat below.
	_ = rc.SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: rc}
	if !s.comment("stream open") {
		return nil
	}
	if ex.Status.Terminal() {
		s.send(eventFinished, streamEvent{ExecutionID: ex.ID, WorkflowID: ex.WorkflowID, At: finishedAt(ex), Status: ex.Status, Error: toErrorInfoDTO(ex.Error)})
		return nil
	}

	beat := time.NewTicker(a.d.Server.SSEHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.drain:
			return nil
		case <-beat.C:
			if !s.comment("keep-alive") {
				return nil
			}
		case e, open := <-sub.Events():
			if !open {
				return nil
			}
			name, payload, ok := toStreamEvent(e)
			if !ok {
				continue
			}
			if name == eventWaiting {
				if cur, err := a.d.Executions.Get(ctx, id); err == nil {
					payload.WaitUntil = cur.WaitUntil
				}
			}
			if !s.send(name, payload) {
				return nil
			}
			if name == eventFinished {
				return nil
			}
		}
	}
}

func finishedAt(ex domain.Execution) time.Time {
	if ex.FinishedAt != nil {
		return *ex.FinishedAt
	}
	return ex.StartedAt
}

// sseWriter writes and flushes SSE frames; a false return means the client is gone.
type sseWriter struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	seq int
}

func (s *sseWriter) flush() bool { return s.rc.Flush() == nil }

func (s *sseWriter) comment(text string) bool {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return false
	}
	return s.flush()
}

func (s *sseWriter) send(event string, payload streamEvent) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return true // cannot happen for these types; skip the frame
	}
	s.seq++
	if _, err := fmt.Fprintf(s.w, "id: %s\nevent: %s\ndata: %s\n\n", strconv.Itoa(s.seq), event, data); err != nil {
		return false
	}
	return s.flush()
}
