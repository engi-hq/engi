package webhook

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

//go:embed chatassets/*
var chatAssetFS embed.FS

// Texts of the hosted widget that are not node parameters.
const (
	widgetPendingText = "Thinking..."
	widgetErrorText   = "Something went wrong. Please try again."
	widgetSendText    = "Send"
	widgetLabelText   = "Message"
)

// chatPage is the widget document. It holds no script or style of its own: the
// JavaScript and CSS are separate same-origin files, so the page's CSP can
// forbid everything inline. Every value is escaped by html/template.
var chatPage = template.Must(template.New("chat").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="widget.css">
</head>
<body>
<main class="chat" id="chat" data-pending="{{.Pending}}" data-error="{{.Error}}">
<header class="chat-header"><h1 class="chat-title">{{.Title}}</h1></header>
<div class="chat-log" id="chat-log" role="log" aria-live="polite" aria-label="{{.Title}}">{{if .Welcome}}<div class="msg msg-bot">{{.Welcome}}</div>{{end}}</div>
<form class="chat-form" id="chat-form" autocomplete="off">
<label class="visually-hidden" for="chat-input">{{.Label}}</label>
<textarea class="chat-input" id="chat-input" rows="1" placeholder="{{.Placeholder}}" required></textarea>
<button class="chat-send" id="chat-send" type="submit">{{.Send}}</button>
</form>
</main>
<script src="widget.js"></script>
</body>
</html>
`))

type chatPageData struct {
	Title, Welcome, Placeholder, Pending, Error, Send, Label string
}

// chatAsset is a static file of the widget.
type chatAsset struct {
	body        []byte
	contentType string
	etag        string
}

// loadChatAssets reads the embedded files and fingerprints them.
func loadChatAssets() (map[string]chatAsset, error) {
	files := map[string]string{
		nodesdk.ChatWidgetScript: "text/javascript; charset=utf-8",
		nodesdk.ChatWidgetStyle:  "text/css; charset=utf-8",
		nodesdk.ChatLoaderScript: "text/javascript; charset=utf-8",
	}
	out := make(map[string]chatAsset, len(files))
	for name, ct := range files {
		body, err := chatAssetFS.ReadFile("chatassets/" + name)
		if err != nil {
			return nil, err //nolint:wrapcheck // embedded files; the name is in the error
		}
		sum := sha256.Sum256(body)
		out[name] = chatAsset{body: body, contentType: ct, etag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	}
	return out, nil
}

// serveChatAsset answers a request for one of the widget's static files.
func (h *Handler) serveChatAsset(w http.ResponseWriter, r *http.Request, name string) {
	a, ok := h.chatAssets[name]
	if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		problem(w, http.StatusNotFound, "Not found", "")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", a.contentType)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("ETag", a.etag)
	// Revalidate on every load: the files are small and change with releases.
	hd.Set("Cache-Control", "no-cache")
	if name == nodesdk.ChatLoaderScript {
		// The loader is included by other sites.
		hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
	}
	if r.Header.Get("If-None-Match") == a.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(a.body)
	}
}

// widgetCSP forbids everything the widget does not need: scripts and styles
// from this origin only, requests to this origin only, and embedding only by
// the node's allowed origins.
func widgetCSP(origins []string) string {
	ancestors := "'self'"
	if len(origins) > 0 {
		ancestors = strings.Join(origins, " ")
	}
	return "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors " + ancestors
}

// serveWidget renders the hosted chat page of a chat trigger.
func (h *Handler) serveWidget(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		problem(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}
	route, ok := h.resolveChat(w, r, path)
	if !ok {
		return
	}
	if route.Chat.Auth != nodesdk.WebhookAuthNone {
		problem(w, http.StatusForbidden, "Widget unavailable", "the hosted widget is only served for chat triggers without authentication")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", widgetCSP(route.Chat.AllowedOrigins))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	data := chatPageData{
		Title: route.Chat.Title, Welcome: route.Chat.Welcome, Placeholder: route.Chat.Placeholder,
		Pending: widgetPendingText, Error: widgetErrorText, Send: widgetSendText, Label: widgetLabelText,
	}
	if err := chatPage.Execute(w, data); err != nil {
		h.d.Logger.Error("rendering the chat widget failed", "path", path, "error", err)
	}
}
