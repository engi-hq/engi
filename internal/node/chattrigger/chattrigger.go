// Package chattrigger implements the Chat trigger node. While its workflow is
// active the host serves a public chat endpoint and a hosted widget for it; each
// message becomes the node's output item {sessionId, chatInput, metadata?}.
package chattrigger

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.chat"

// Credential slots, one per authentication kind.
const (
	credBasic  = "basicAuth"
	credHeader = "headerAuth"
)

// Defaults of the optional parameters.
const (
	defaultReplyField  = "output"
	defaultTitle       = "Chat"
	defaultPlaceholder = "Type a message"
)

// Node is the Chat trigger.
type Node struct{}

var _ nodesdk.ChatTrigger = Node{}

// New returns a Chat trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Chat Trigger", Group: nodesdk.GroupTrigger, Icon: "message-circle",
		Description: "Starts the workflow for each chat message and answers with the workflow's reply.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{
			{Name: credBasic, Type: "engi.http.basic"},
			{Name: credHeader, Type: "engi.http.header"},
		},
		Properties: []nodesdk.Property{
			{Name: "path", DisplayName: "Path", Type: nodesdk.PropString, Placeholder: "support",
				Description: "Relative to the chat base path. Empty uses <workflow id>/<node name>. The widget is served at <path>/widget."},
			{Name: "responseMode", DisplayName: "Respond", Type: nodesdk.PropOptions, Default: nodesdk.ResponseLastNode, Options: []nodesdk.Option{
				{Name: "When the last node finishes", Value: nodesdk.ResponseLastNode},
				{Name: "Using a Respond to Webhook node", Value: nodesdk.ResponseNode}}},
			{Name: "replyField", DisplayName: "Reply field", Type: nodesdk.PropString, Default: defaultReplyField,
				Description:    "Field of the last node's first item that holds the reply. Falls back to \"text\".",
				DisplayOptions: nodekit.Show("responseMode", nodesdk.ResponseLastNode)},
			{Name: "authentication", DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: nodesdk.WebhookAuthNone,
				Description: "The hosted widget is only available without authentication.",
				Options: []nodesdk.Option{
					{Name: "None", Value: nodesdk.WebhookAuthNone}, {Name: "Basic auth", Value: nodesdk.WebhookAuthBasic},
					{Name: "Header token", Value: nodesdk.WebhookAuthHeader}}},
			{Name: "allowedOrigins", DisplayName: "Allowed origins", Type: nodesdk.PropString, Placeholder: "https://example.com",
				Description: "Comma-separated sites that may call the chat from a browser and embed the widget. * allows any. Empty allows this server only."},
			{Name: "title", DisplayName: "Title", Type: nodesdk.PropString, Default: defaultTitle},
			{Name: "welcomeMessage", DisplayName: "Welcome message", Type: nodesdk.PropString,
				Description: "Shown at the top of the widget before the first message."},
			{Name: "inputPlaceholder", DisplayName: "Input placeholder", Type: nodesdk.PropString, Default: defaultPlaceholder},
		},
	}
}

// Execute implements nodesdk.Node. The host builds the trigger item; the node
// forwards it.
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// Webhook implements nodesdk.WebhookTrigger: the route the host registers.
func (n Node) Webhook(params map[string]any) (nodesdk.WebhookSpec, error) {
	c, err := n.Chat(params)
	if err != nil {
		return nodesdk.WebhookSpec{}, err
	}
	return nodesdk.WebhookSpec{
		Method: nodesdk.WebhookMethodChat, Path: c.Path, ResponseMode: c.ResponseMode,
		Auth: c.Auth, AuthCredential: c.AuthCredential,
	}, nil
}

// Chat implements nodesdk.ChatTrigger. params are the node parameters as saved,
// so expressions are not evaluated here.
func (Node) Chat(params map[string]any) (nodesdk.ChatSpec, error) {
	spec := nodesdk.ChatSpec{
		Path:         strings.Trim(str(params, "path", ""), "/"),
		ResponseMode: str(params, "responseMode", nodesdk.ResponseLastNode),
		Auth:         str(params, "authentication", nodesdk.WebhookAuthNone),
		ReplyField:   str(params, "replyField", defaultReplyField),
		Title:        str(params, "title", defaultTitle),
		Welcome:      str(params, "welcomeMessage", ""),
		Placeholder:  str(params, "inputPlaceholder", defaultPlaceholder),
	}
	if !slices.Contains([]string{nodesdk.ResponseLastNode, nodesdk.ResponseNode}, spec.ResponseMode) {
		return nodesdk.ChatSpec{}, fmt.Errorf("responseMode %q is not supported", spec.ResponseMode)
	}
	switch spec.Auth {
	case nodesdk.WebhookAuthNone:
	case nodesdk.WebhookAuthBasic:
		spec.AuthCredential = credBasic
	case nodesdk.WebhookAuthHeader:
		spec.AuthCredential = credHeader
	default:
		return nodesdk.ChatSpec{}, fmt.Errorf("authentication %q is not supported", spec.Auth)
	}
	if strings.Contains(spec.Path, ":") {
		return nodesdk.ChatSpec{}, fmt.Errorf("path %q: chat paths cannot have parameters", spec.Path)
	}
	if spec.Path != "" {
		segs := strings.Split(spec.Path, "/")
		if last := segs[len(segs)-1]; nodesdk.ChatReservedSegment(last) {
			return nodesdk.ChatSpec{}, fmt.Errorf("path %q must not end in %q, which is served by the host", spec.Path, last)
		}
	}
	for _, o := range strings.Split(str(params, "allowedOrigins", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			spec.AllowedOrigins = append(spec.AllowedOrigins, strings.TrimRight(o, "/"))
		}
	}
	return spec, nil
}

func str(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}
