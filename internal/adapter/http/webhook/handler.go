// Package webhook is the HTTP adapter for webhook triggers: production calls
// (/hook/...), test calls (/hook-test/...) and wait-node resume calls
// (/hook-wait/<token>). It turns a request into the trigger item, starts the
// execution through the execution service and answers according to the node's
// response mode.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/trigger"
)

// Router resolves a call to the trigger node that serves it; *trigger.Service
// implements it. Both methods wrap domain.ErrNotFound when nothing matches.
type Router interface {
	ResolveLive(ctx context.Context, method, path string) (trigger.Route, error)
	ResolveTest(ctx context.Context, method, path string) (trigger.Route, error)
}

// Executions starts and awaits executions; *execution.Service implements it.
type Executions interface {
	Start(ctx context.Context, req execution.StartRequest) (domain.ExecutionID, error)
	RunSync(ctx context.Context, req execution.StartRequest) (domain.Execution, error)
	Await(ctx context.Context, id domain.ExecutionID) (domain.Execution, error)
}

// Resumer resumes a waiting execution; *waiter.Service implements it.
type Resumer interface {
	ResumeByToken(ctx context.Context, token string, items []domain.Item) error
}

// Uploads stores files posted to a webhook; *blob.Service implements it.
type Uploads interface {
	Put(ctx context.Context, execution domain.ExecutionID, name, mime string, r io.Reader) (domain.BinaryRef, error)
	DeleteExecution(ctx context.Context, execution domain.ExecutionID) error
}

// ExecutionIDs mints the id an execution will get, so uploads can be stored
// under it before the execution exists.
type ExecutionIDs interface {
	ExecutionID() (domain.ExecutionID, error)
}

// Deps are the collaborators of the Handler. Blobs, IDs and Logger are optional;
// without Blobs (or IDs) file uploads are refused with 415.
type Deps struct {
	Router      Router
	Executions  Executions
	Resumer     Resumer
	Credentials port.CredentialResolver
	Bus         port.Bus
	Blobs       Uploads
	IDs         ExecutionIDs
	Logger      *slog.Logger
	// Chat, when set, switches on the chat endpoints and widgets of Chat triggers.
	Chat *ChatDeps
}

// Handler serves the webhook endpoints. It is safe for concurrent use.
type Handler struct {
	cfg        config.WebhookConfig
	d          Deps
	chatAssets map[string]chatAsset
}

var _ http.Handler = (*Handler)(nil)

// New builds a Handler.
func New(cfg config.WebhookConfig, d Deps) (*Handler, error) {
	if d.Router == nil || d.Executions == nil || d.Resumer == nil || d.Credentials == nil || d.Bus == nil {
		return nil, errors.New("webhook handler requires router, executions, resumer, credentials and bus")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &Handler{cfg: cfg, d: d}
	if d.Chat != nil {
		if d.Chat.Router == nil {
			return nil, errors.New("chat endpoints require a chat router")
		}
		assets, err := loadChatAssets()
		if err != nil {
			return nil, fmt.Errorf("loading chat widget files: %w", err)
		}
		h.chatAssets = assets
	}
	return h, nil
}

// Patterns returns the ServeMux patterns the handler serves, one per base path
// (production, test, wait), for example "/hook/". Mount the handler on each.
func (h *Handler) Patterns() []string {
	p := []string{h.cfg.BasePath + "/", h.cfg.TestBasePath + "/", h.cfg.WaitBasePath + "/"}
	if h.d.Chat != nil {
		p = append(p, h.d.Chat.Config.BasePath+"/")
	}
	return p
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case h.d.Chat != nil && strings.HasPrefix(p, h.d.Chat.Config.BasePath+"/"):
		h.serveChat(w, r, strings.TrimPrefix(p, h.d.Chat.Config.BasePath+"/"))
	case strings.HasPrefix(p, h.cfg.WaitBasePath+"/"):
		h.serveWait(w, r, strings.TrimPrefix(p, h.cfg.WaitBasePath+"/"))
	case strings.HasPrefix(p, h.cfg.TestBasePath+"/"):
		h.serveHook(w, r, strings.TrimPrefix(p, h.cfg.TestBasePath+"/"), true)
	case strings.HasPrefix(p, h.cfg.BasePath+"/"):
		h.serveHook(w, r, strings.TrimPrefix(p, h.cfg.BasePath+"/"), false)
	default:
		problem(w, http.StatusNotFound, "Not found", "")
	}
}

// cleanupTimeout bounds deleting uploads of an execution that did not start.
const cleanupTimeout = 30 * time.Second

func (h *Handler) serveHook(w http.ResponseWriter, r *http.Request, path string, test bool) {
	ctx := r.Context()
	resolve := h.d.Router.ResolveLive
	if test {
		resolve = h.d.Router.ResolveTest
	}
	route, err := resolve(ctx, r.Method, path)
	if errors.Is(err, domain.ErrNotFound) {
		problem(w, http.StatusNotFound, "Not found", "")
		return
	}
	if err != nil {
		h.d.Logger.Error("resolving webhook failed", "method", r.Method, "path", path, "error", err)
		problem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	// Authenticate before the body is read in full and before any execution exists.
	auth, ok := h.authenticate(ctx, r, route)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="webhook"`)
		problem(w, http.StatusUnauthorized, "Unauthorized", "")
		return
	}
	var execID domain.ExecutionID
	if h.d.Blobs != nil && h.d.IDs != nil {
		if execID, err = h.d.IDs.ExecutionID(); err != nil {
			h.fail(w, "creating the execution id failed", err)
			return
		}
	}
	item, status, err := h.buildItem(w, r, route, auth, execID)
	if err != nil {
		h.dropUploads(ctx, execID)
		problem(w, status, http.StatusText(status), err.Error())
		return
	}
	req := execution.StartRequest{
		WorkflowID: route.WorkflowID, Mode: route.Mode, StartNode: route.Node, Items: []domain.Item{item}, ExecutionID: execID,
	}
	if hdr := h.cfg.IdempotencyHeader; hdr != "" && !route.Test {
		if key := strings.TrimSpace(r.Header.Get(hdr)); key != "" {
			req.DedupKey = route.Node + ":" + key
		}
	}
	if len(item.Binary) == 0 {
		h.answer(w, r, route, req)
		return
	}
	// Uploads belong to the execution this request starts; drop them when it
	// was not started (failure, or a duplicate delivery that reuses another).
	guard := &startGuard{Executions: h.d.Executions}
	scoped := *h
	scoped.d.Executions = guard
	scoped.answer(w, r, route, req)
	if guard.notStarted {
		h.dropUploads(ctx, execID)
	}
}

// dropUploads deletes the files stored for an execution that never started.
func (h *Handler) dropUploads(ctx context.Context, id domain.ExecutionID) {
	if id == "" || h.d.Blobs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := h.d.Blobs.DeleteExecution(ctx, id); err != nil {
		h.d.Logger.Warn("deleting unused uploads failed", "execution", id, "error", err)
	}
}

// startGuard records when starting the execution definitely did not happen.
// Anything unclear (a timeout while waiting, say) keeps the uploads: the
// execution may be running, and the orphan sweep is the safety net.
type startGuard struct {
	Executions
	notStarted bool
}

func (g *startGuard) Start(ctx context.Context, req execution.StartRequest) (domain.ExecutionID, error) {
	id, err := g.Executions.Start(ctx, req)
	g.notStarted = g.notStarted || err != nil
	return id, err //nolint:wrapcheck // passthrough
}

func (g *startGuard) RunSync(ctx context.Context, req execution.StartRequest) (domain.Execution, error) {
	ex, err := g.Executions.RunSync(ctx, req)
	var dup *domain.DuplicateError
	var invalid *domain.ValidationError
	g.notStarted = g.notStarted || errors.As(err, &dup) || errors.As(err, &invalid)
	return ex, err //nolint:wrapcheck // passthrough
}

// problem writes an RFC 9457 problem document.
func problem(w http.ResponseWriter, status int, title, detail string) {
	doc := map[string]any{"type": "about:blank", "title": title, "status": status}
	if detail != "" {
		doc["detail"] = detail
	}
	writeJSON(w, status, "application/problem+json", doc)
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// duplicateOf returns the existing execution when err reports a repeated delivery.
func duplicateOf(err error) (domain.ExecutionID, bool) {
	var dup *domain.DuplicateError
	if errors.As(err, &dup) && dup.ExistingID != "" {
		return dup.ExistingID, true
	}
	return "", false
}

func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve), errors.Is(err, domain.ErrNotFound):
		problem(w, http.StatusConflict, "Workflow cannot run", "")
	default:
		problem(w, http.StatusInternalServerError, "Internal error", "")
	}
	h.d.Logger.Error(what, "error", err)
}
