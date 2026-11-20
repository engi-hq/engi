package api

import (
	"context"
	"errors"
	"net/http"
)

type statusDTO struct {
	Status string `json:"status"`
}

// Health states.
const (
	statusOK       = "ok"
	statusDraining = "draining"
)

// health is the liveness probe: the process answers.
func (a *API) health(w http.ResponseWriter, r *http.Request) error {
	return a.writeJSON(w, r, http.StatusOK, statusDTO{Status: statusOK})
}

// errNotReady is the detail-free cause of a failed readiness probe.
var errNotReady = errors.New("not ready")

// ready is the readiness probe. It fails while draining (SIGTERM received) and
// when the store is unreachable. The cause is logged, not returned.
func (a *API) ready(w http.ResponseWriter, r *http.Request) error {
	if a.draining() {
		w.Header().Set("Retry-After", "1")
		return a.writeJSON(w, r, http.StatusServiceUnavailable, statusDTO{Status: statusDraining})
	}
	if a.d.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), a.d.Server.HealthTimeout)
		defer cancel()
		if err := a.d.Ready(ctx); err != nil {
			a.d.Logger.WarnContext(r.Context(), "readiness check failed", "error", err)
			return errors.Join(errNotReady, &requestError{status: http.StatusServiceUnavailable, detail: "a dependency is not ready"})
		}
	}
	return a.writeJSON(w, r, http.StatusOK, statusDTO{Status: statusOK})
}
