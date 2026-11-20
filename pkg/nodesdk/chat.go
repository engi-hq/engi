package nodesdk

// WebhookMethodChat is the pseudo HTTP method chat triggers register their
// route under. Real requests never carry it, so chat paths form their own
// namespace and can never be reached through the webhook endpoints.
const WebhookMethodChat = "CHAT"

// ChatSpec says how a chat trigger is served. The host reads it from the node's
// parameters when the workflow is activated and when the widget is rendered.
type ChatSpec struct {
	// Path is relative to the chat base path. Empty means the host picks one.
	Path string
	// ResponseMode is ResponseLastNode or ResponseNode.
	ResponseMode string
	// Auth is one of the WebhookAuth constants; the credential is bound to the
	// node under AuthCredential.
	Auth           string
	AuthCredential string
	// ReplyField names the field of the last node's first item that holds the
	// reply (lastNode mode). The host falls back to "text" when it is missing.
	ReplyField string
	// AllowedOrigins lists the sites that may call the endpoint from a browser
	// and embed the widget. "*" allows any. Empty allows the serving origin only.
	AllowedOrigins []string
	// Title, Welcome and Placeholder customize the hosted widget.
	Title       string
	Welcome     string
	Placeholder string
}

// ChatTrigger is a webhook trigger that serves a chat endpoint and widget.
// Webhook must report Method WebhookMethodChat.
type ChatTrigger interface {
	WebhookTrigger
	Chat(params map[string]any) (ChatSpec, error)
}

// Names the host serves below a chat path (the widget page and its assets). A
// chat path may not end in one of them.
const (
	ChatWidgetSegment = "widget"
	ChatWidgetScript  = "widget.js"
	ChatWidgetStyle   = "widget.css"
	ChatLoaderScript  = "loader.js"
)

// ChatReservedSegment reports whether seg is a name the host serves below a
// chat path.
func ChatReservedSegment(seg string) bool {
	switch seg {
	case ChatWidgetSegment, ChatWidgetScript, ChatWidgetStyle, ChatLoaderScript:
		return true
	}
	return false
}
