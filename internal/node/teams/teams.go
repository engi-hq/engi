// Package teams implements the Microsoft Teams node (engi.chat.teams) over the
// shared HTTP client. Webhook mode posts a message card or Adaptive Card to an
// incoming webhook; Graph mode posts a channel message with an OAuth2
// credential.
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/engi-hq/engi/internal/credtype/oauth2"
	"github.com/engi-hq/engi/internal/credtype/teamswebhook"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.chat.teams"

// Modes.
const (
	modeWebhook = "webhook"
	modeGraph   = "graph"
)

// Credential keys in the node's credentials map.
const (
	credWebhook = "webhook"
	credGraph   = "graph"
)

// ErrTeams is wrapped by every error Teams or Graph reports.
var ErrTeams = errors.New("teams API error")

// Config is what the node needs from nodes.chat.*.
type Config struct {
	GraphBaseURL string
	Caller       chatkit.Caller
}

// Node is the Teams node.
type Node struct{ cfg Config }

// New returns a Teams node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Microsoft Teams", Group: nodesdk.GroupAction, Icon: "message-square",
		Description: "Send a card through a Teams incoming webhook, or post a channel message through Microsoft Graph.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{
			{Name: credWebhook, Type: teamswebhook.Name},
			{Name: credGraph, Type: oauth2.Name},
		},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: modeWebhook, Options: []nodesdk.Option{
				nodekit.Opt(modeWebhook, "Incoming webhook"), nodekit.Opt(modeGraph, "Graph: post channel message")}},
			{Name: "text", DisplayName: "Text", Type: nodesdk.PropString, Description: "Plain message. Ignored in webhook mode when a card is given."},
			{Name: "card", DisplayName: "Card (JSON)", Type: nodesdk.PropJSON, DisplayOptions: nodekit.Show("mode", modeWebhook),
				Description: "A MessageCard or an Adaptive Card object."},
			{Name: "teamId", DisplayName: "Team ID", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("mode", modeGraph)},
			{Name: "channelId", DisplayName: "Channel ID", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("mode", modeGraph)},
			{Name: "contentType", DisplayName: "Content type", Type: nodesdk.PropOptions, Default: "text", DisplayOptions: nodekit.Show("mode", modeGraph),
				Options: []nodesdk.Option{nodekit.Opt("text", "Text"), nodekit.Opt("html", "HTML")}},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	credName := credWebhook
	switch mode {
	case modeWebhook:
	case modeGraph:
		credName = credGraph
	default:
		return nodesdk.Output{}, fmt.Errorf("unknown mode %q", mode)
	}
	cred, err := in.Credential(ctx, credName)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credName, err)
	}
	secrets := []string{chatkit.Secret(cred, "url"), chatkit.Secret(cred, oauth2.FieldAccess)}
	if u, err := url.Parse(chatkit.Secret(cred, "url")); err == nil {
		// The webhook path and query hold the secret; hide them on their own too.
		secrets = append(secrets, strings.Trim(u.Path, "/"), u.RawQuery)
	}
	out := make([]nodesdk.Item, 0, len(items))
	for i := range items {
		var res map[string]any
		var err error
		if mode == modeWebhook {
			res, err = n.webhook(ctx, in, cred, secrets, i)
		} else {
			res, err = n.graph(ctx, in, cred, secrets, i)
		}
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

const adaptiveContentType = "application/vnd.microsoft.card.adaptive"

func (n *Node) webhook(ctx context.Context, in nodesdk.Input, cred nodesdk.Credential, secrets []string, i int) (map[string]any, error) {
	card, err := nodekit.ParamMap(ctx, in, "card", i)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	switch {
	case card["type"] == "AdaptiveCard":
		payload = map[string]any{"type": "message", "attachments": []any{map[string]any{"contentType": adaptiveContentType, "content": card}}}
	case len(card) > 0:
		payload = card
	default:
		text, err := nodesdk.ParamString(ctx, in, "text", i)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("text or card is required")
		}
		payload = map[string]any{"@type": "MessageCard", "@context": "http://schema.org/extensions", "text": text}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding card: %w", err)
	}
	resp, err := n.cfg.Caller.Do(ctx, in.HTTP(), func(ctx context.Context) (*http.Request, error) {
		// The credential rewrites this placeholder to the webhook URL.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://webhook.invalid/", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		return req, cred.Authenticate(req)
	}, secrets...)
	if err != nil {
		return nil, fmt.Errorf("teams webhook: %w", err)
	}
	if resp.Status/100 != 2 {
		return nil, &nodesdk.NodeError{Message: "teams webhook: " + snippet(resp.Body, secrets), HTTPStatus: resp.Status, Err: ErrTeams}
	}
	return map[string]any{"status": float64(resp.Status)}, nil
}

func (n *Node) graph(ctx context.Context, in nodesdk.Input, cred nodesdk.Credential, secrets []string, i int) (map[string]any, error) {
	get := func(name string) (string, error) {
		v, err := nodesdk.ParamString(ctx, in, name, i)
		if err == nil && strings.TrimSpace(v) == "" && name != "contentType" {
			err = fmt.Errorf("%s must not be empty", name)
		}
		return v, err
	}
	team, err := get("teamId")
	if err != nil {
		return nil, err
	}
	channel, err := get("channelId")
	if err != nil {
		return nil, err
	}
	text, err := get("text")
	if err != nil {
		return nil, err
	}
	ctype, err := get("contentType")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"body": map[string]any{"contentType": ctype, "content": text}})
	if err != nil {
		return nil, fmt.Errorf("encoding message: %w", err)
	}
	resp, err := n.cfg.Caller.Do(ctx, in.HTTP(), func(ctx context.Context) (*http.Request, error) {
		u := strings.TrimRight(n.cfg.GraphBaseURL, "/") + "/teams/" + url.PathEscape(team) + "/channels/" + url.PathEscape(channel) + "/messages"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		return req, cred.Authenticate(req)
	}, secrets...)
	if err != nil {
		return nil, fmt.Errorf("teams graph: %w", err)
	}
	var res map[string]any
	_ = json.Unmarshal(resp.Body, &res)
	if resp.Status/100 != 2 {
		msg := snippet(resp.Body, secrets)
		if e, ok := res["error"].(map[string]any); ok {
			code, _ := e["code"].(string)
			m, _ := e["message"].(string)
			msg = chatkit.Redact(strings.TrimSpace(code+": "+m), secrets...)
		}
		return nil, &nodesdk.NodeError{Message: "teams graph: " + msg, HTTPStatus: resp.Status, Err: ErrTeams}
	}
	if res == nil {
		res = map[string]any{}
	}
	return res, nil
}

// maxErrorSnippet bounds how much of an error body reaches an error message.
const maxErrorSnippet = 200

func snippet(body []byte, secrets []string) string {
	s := strings.TrimSpace(string(body))
	if len(s) > maxErrorSnippet {
		s = s[:maxErrorSnippet]
	}
	if s == "" {
		return "request failed"
	}
	return chatkit.Redact(s, secrets...)
}
