package webhook

import (
	"errors"
	"net/http"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/internal/service/waiter"
)

// serveWait resumes the execution that waits for token. The call's headers,
// query and body become the item the Wait node hands on. An unknown or wrong
// token is a plain 404: it says nothing about which executions exist.
func (h *Handler) serveWait(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		problem(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}
	item, status, err := h.buildItem(w, r, trigger.Route{}, authResult{}, "")
	if err != nil {
		problem(w, status, http.StatusText(status), err.Error())
		return
	}
	delete(item.JSON, "params")
	err = h.d.Resumer.ResumeByToken(r.Context(), token, []domain.Item{item})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, "application/json", map[string]any{"resumed": true})
	case errors.Is(err, waiter.ErrInvalidToken):
		problem(w, http.StatusNotFound, "Not found", "")
	case errors.Is(err, waiter.ErrNotWaiting):
		problem(w, http.StatusConflict, "The execution is not waiting any more", "")
	default:
		h.d.Logger.Error("resuming by token failed", "error", err)
		problem(w, http.StatusInternalServerError, "Internal error", "")
	}
}
