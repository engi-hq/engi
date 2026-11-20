package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ChatRouter resolves a chat path to the chat trigger that serves it;
// *trigger.Service implements it. It wraps domain.ErrNotFound when no active
// workflow serves the path.
type ChatRouter interface {
	ResolveChat(ctx context.Context, path string) (trigger.ChatRoute, error)
}

// ChatDeps switches the chat endpoints on. Without it the handler serves none.
type ChatDeps struct {
	Config config.ChatTriggerConfig
	Router ChatRouter
	// SessionIDs mints a session id for a caller that sent none. Nil uses
	// 128 random bits in hex.
	SessionIDs func() (string, error)
}

// sessionHeader carries the session id on every answer, so callers that use a
// Respond to Webhook node (which writes the body) can still learn it.
const sessionHeader = "X-Chat-Session-Id"

// chatSessionID is what a client-supplied session id may look like.
var chatSessionID = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// chatRequest is the body of a chat message. Message is the canonical field;
// ChatInput is accepted as an alias.
type chatRequest struct {
	SessionID string         `json:"sessionId"`
	Message   *string        `json:"message"`
	ChatInput *string        `json:"chatInput"`
	Metadata  map[string]any `json:"metadata"`
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// serveChat routes a request below the chat base path: the widget page, its
// assets, or the message endpoint.
func (h *Handler) serveChat(w http.ResponseWriter, r *http.Request, rest string) {
	rest = domain.NormalizeWebhookPath(rest)
	prefix, last := "", rest
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		prefix, last = rest[:i], rest[i+1:]
	}
	if nodesdk.ChatReservedSegment(last) {
		if last == nodesdk.ChatWidgetSegment {
			h.serveWidget(w, r, prefix)
		} else {
			h.serveChatAsset(w, r, last)
		}
		return
	}
	h.serveMessage(w, r, rest)
}

// resolveChat finds the chat route, answering 404 or 500 itself on failure.
func (h *Handler) resolveChat(w http.ResponseWriter, r *http.Request, path string) (trigger.ChatRoute, bool) {
	route, err := h.d.Chat.Router.ResolveChat(r.Context(), path)
	if errors.Is(err, domain.ErrNotFound) {
		problem(w, http.StatusNotFound, "Not found", "")
		return trigger.ChatRoute{}, false
	}
	if err != nil {
		h.d.Logger.Error("resolving chat trigger failed", "path", path, "error", err)
		problem(w, http.StatusInternalServerError, "Internal error", "")
		return trigger.ChatRoute{}, false
	}
	return route, true
}

func (h *Handler) serveMessage(w http.ResponseWriter, r *http.Request, path string) {
	if path == "" {
		problem(w, http.StatusNotFound, "Not found", "")
		return
	}
	cfg := h.d.Chat.Config
	route, ok := h.resolveChat(w, r, path)
	if !ok {
		return
	}
	if !h.applyCORS(w, r, route.Chat.AllowedOrigins) {
		problem(w, http.StatusForbidden, "Origin not allowed", "")
		return
	}
	switch r.Method {
	case http.MethodOptions:
		h.preflight(w, r)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		problem(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}
	if _, ok := h.authenticate(r.Context(), r, route.Route); !ok {
		if route.Chat.Auth == nodesdk.WebhookAuthBasic {
			w.Header().Set("WWW-Authenticate", `Basic realm="chat"`)
		}
		problem(w, http.StatusUnauthorized, "Unauthorized", "")
		return
	}
	req, status, err := h.readChatRequest(w, r, cfg)
	if err != nil {
		problem(w, status, http.StatusText(status), err.Error())
		return
	}
	sid := req.SessionID
	if sid == "" {
		mint := h.d.Chat.SessionIDs
		if mint == nil {
			mint = newSessionID
		}
		if sid, err = mint(); err != nil {
			h.d.Logger.Error("minting a chat session id failed", "error", err)
			problem(w, http.StatusInternalServerError, "Internal error", "")
			return
		}
	}
	w.Header().Set(sessionHeader, sid)
	item := map[string]any{"sessionId": sid, "chatInput": *req.Message}
	if req.Metadata != nil {
		item["metadata"] = req.Metadata
	}
	start := execution.StartRequest{
		WorkflowID: route.WorkflowID, Mode: route.Mode, StartNode: route.Node, Items: []domain.Item{{JSON: item}},
	}
	// The chat's own timeout bounds the wait; everything else is the webhook's.
	scoped := *h
	scoped.cfg.ResponseTimeout = cfg.ResponseTimeout
	switch route.Spec.ResponseMode {
	case nodesdk.ResponseNode:
		scoped.answerViaNode(w, r, route.Route, start)
	default:
		scoped.answerChatLastNode(w, r, route, start, sid)
	}
}

// readChatRequest reads and validates the JSON body.
func (h *Handler) readChatRequest(w http.ResponseWriter, r *http.Request, cfg config.ChatTriggerConfig) (chatRequest, int, error) {
	var req chatRequest
	if r.ContentLength > cfg.MaxBodyBytes {
		return req, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", cfg.MaxBodyBytes)
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return req, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", cfg.MaxBodyBytes)
	case err != nil:
		return req, http.StatusBadRequest, errors.New("reading the request body failed")
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, http.StatusBadRequest, errors.New(`the body must be a JSON object like {"sessionId": "...", "message": "..."}`)
	}
	if (req.Message == nil || strings.TrimSpace(*req.Message) == "") && (req.ChatInput == nil || strings.TrimSpace(*req.ChatInput) == "") {
		return req, http.StatusBadRequest, errors.New("message must be a non-empty string")
	}
	if req.Message == nil || strings.TrimSpace(*req.Message) == "" {
		req.Message = req.ChatInput
	}
	if req.SessionID != "" && (len(req.SessionID) > cfg.MaxSessionIDLength || !chatSessionID.MatchString(req.SessionID)) {
		return req, http.StatusBadRequest, fmt.Errorf("sessionId must be at most %d characters of letters, digits and . _ : -", cfg.MaxSessionIDLength)
	}
	return req, http.StatusOK, nil
}

// applyCORS sets the response headers for a browser call and reports whether
// the call's origin may proceed. Calls without an Origin header (servers,
// curl) and calls from the serving origin always may.
func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	w.Header().Add("Vary", "Origin")
	if u, err := url.Parse(origin); err == nil && u.Host == r.Host {
		return true
	}
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(a, origin) {
			hd := w.Header()
			hd.Set("Access-Control-Allow-Origin", origin)
			hd.Set("Access-Control-Expose-Headers", sessionHeader)
			return true
		}
	}
	return false
}

// preflight answers a CORS preflight; applyCORS has already vetted the origin.
func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	if hd.Get("Access-Control-Allow-Origin") != "" {
		hd.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			hd.Add("Vary", "Access-Control-Request-Headers")
			hd.Set("Access-Control-Allow-Headers", req)
		}
		hd.Set("Access-Control-Max-Age", strconv.Itoa(int(h.d.Chat.Config.PreflightMaxAge.Seconds())))
	}
	w.WriteHeader(http.StatusNoContent)
}

// answerChatLastNode holds the call until the execution ends and replies with
// the reply field of the last node's first item.
func (h *Handler) answerChatLastNode(w http.ResponseWriter, r *http.Request, route trigger.ChatRoute, req execution.StartRequest, sid string) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.ResponseTimeout)
	defer cancel()
	ex, err := h.d.Executions.RunSync(ctx, req)
	if err != nil {
		h.syncFailure(w, r, err)
		return
	}
	switch ex.Status {
	case domain.StatusSuccess:
	case domain.StatusWaiting:
		writeJSON(w, defaultImmediateStatus, "application/json", map[string]any{"sessionId": sid, "executionId": ex.ID, "status": ex.Status})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, "application/problem+json", map[string]any{
			"type": "about:blank", "title": "Workflow execution failed", "status": http.StatusInternalServerError, "executionId": ex.ID,
		})
		return
	}
	reply, ok := chatReply(ex.LastOutput(), route.Chat.ReplyField)
	if !ok {
		writeJSON(w, http.StatusBadGateway, "application/problem+json", map[string]any{
			"type": "about:blank", "title": "The workflow produced no reply", "status": http.StatusBadGateway, "executionId": ex.ID,
			"detail": fmt.Sprintf("the last node's first item has no %q or %q field", route.Chat.ReplyField, fallbackReplyField),
		})
		return
	}
	writeJSON(w, http.StatusOK, "application/json", map[string]any{"sessionId": sid, "output": reply, "executionId": ex.ID})
}

// fallbackReplyField is read when the configured reply field is missing.
const fallbackReplyField = "text"

// chatReply extracts the reply text from the first item: the configured field,
// else "text". Strings are used as they are, other values as JSON.
func chatReply(items []domain.Item, field string) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	for _, name := range []string{field, fallbackReplyField} {
		v, ok := items[0].JSON[name]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			return s, true
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		return string(b), true
	}
	return "", false
}
