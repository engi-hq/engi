// Package mcpserver is the HTTP adapter of the MCP server trigger. It serves
// the Streamable HTTP transport of the Model Context Protocol at /mcp/<path>,
// outside the REST API's session and API-key authentication: each endpoint
// authenticates callers with the credential bound to its trigger node. It
// checks the caller and the request size, then hands the JSON-RPC body to the
// protocol dispatcher in internal/mcp.
//
// The server is stateless. POST answers with application/json; there is no
// event stream, so GET and DELETE are answered with 405 as the spec allows.
package mcpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/mcpserver"
)

// Endpoints finds the MCP endpoint serving a path; *mcpserver.Service
// implements it. Open wraps domain.ErrNotFound when none exists.
type Endpoints interface {
	Open(ctx context.Context, path string) (*mcpserver.Endpoint, error)
}

// Deps are the collaborators of the Handler. Logger is optional.
type Deps struct {
	Endpoints   Endpoints
	Credentials port.CredentialResolver
	Logger      *slog.Logger
}

// Handler serves the MCP endpoints. It is safe for concurrent use.
type Handler struct {
	cfg config.MCPServerConfig
	d   Deps
}

var _ http.Handler = (*Handler)(nil)

// New builds a Handler.
func New(cfg config.MCPServerConfig, d Deps) (*Handler, error) {
	if d.Endpoints == nil || d.Credentials == nil {
		return nil, errors.New("MCP handler requires endpoints and credentials")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Handler{cfg: cfg, d: d}, nil
}

// Patterns returns the ServeMux pattern the handler serves, for example
// "/mcp/".
func (h *Handler) Patterns() []string { return []string{h.cfg.BasePath + "/"} }

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		problem(w, http.StatusForbidden, "Forbidden", "")
		return
	}
	path := domain.NormalizeWebhookPath(strings.TrimPrefix(r.URL.Path, h.cfg.BasePath+"/"))
	ep, err := h.d.Endpoints.Open(r.Context(), path)
	if errors.Is(err, domain.ErrNotFound) {
		problem(w, http.StatusNotFound, "Not found", "")
		return
	}
	if err != nil {
		h.d.Logger.Error("opening MCP endpoint failed", "path", path, "error", err)
		problem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	// Authenticate before the body is read.
	if !h.authenticate(r, ep) {
		if ep.Auth == mcp.ServerAuthBearer {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		problem(w, http.StatusUnauthorized, "Unauthorized", "")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		problem(w, http.StatusMethodNotAllowed, "Method not allowed", "This MCP server answers POST only.")
		return
	}
	if v := r.Header.Get(mcp.HeaderProtocolVersion); v != "" && !mcp.SupportedVersion(v) {
		problem(w, http.StatusBadRequest, "Unsupported MCP protocol version", v)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.cfg.MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "Request body too large", "")
			return
		}
		problem(w, http.StatusBadRequest, "Bad request", "reading the body failed")
		return
	}
	reply := mcp.NewServer(mcp.Implementation{Name: h.cfg.ServerName}, ep).Handle(r.Context(), body)
	switch {
	case reply.Accepted:
		w.WriteHeader(http.StatusAccepted)
	case reply.Invalid:
		writeBody(w, http.StatusBadRequest, reply.Body)
	default:
		writeBody(w, http.StatusOK, reply.Body)
	}
}

// originAllowed applies the DNS-rebinding rule: a request that names an Origin
// must name an allowed one; requests without it (non-browser clients) pass.
func (h *Handler) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || slices.Contains(h.cfg.AllowedOrigins, origin)
}

// authenticate checks the call against the endpoint's authentication option. A
// refusal says nothing about why.
func (h *Handler) authenticate(r *http.Request, ep *mcpserver.Endpoint) bool {
	if ep.Auth == "" || ep.Auth == mcp.ServerAuthNone {
		return true
	}
	if ep.Auth != mcp.ServerAuthBearer && ep.Auth != mcp.ServerAuthHeader {
		return false
	}
	cred, err := h.d.Credentials.Resolve(r.Context(), ep.Credential)
	if err != nil {
		h.d.Logger.Error("loading the MCP credential failed", "workflow", ep.WorkflowID, "node", ep.Node, "error", err)
		return false
	}
	var got, want string
	if ep.Auth == mcp.ServerAuthBearer {
		want, _ = cred.Data["token"].(string)
		if scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
			got = strings.TrimSpace(token)
		}
	} else {
		name, _ := cred.Data["name"].(string)
		want, _ = cred.Data["value"].(string)
		if name == "" {
			return false
		}
		got = r.Header.Get(name)
	}
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeBody(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// problem writes an RFC 9457 problem document.
func problem(w http.ResponseWriter, status int, title, detail string) {
	doc := map[string]any{"type": "about:blank", "title": title, "status": status}
	if detail != "" {
		doc["detail"] = detail
	}
	body, err := json.Marshal(doc)
	if err != nil {
		http.Error(w, title, status)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
