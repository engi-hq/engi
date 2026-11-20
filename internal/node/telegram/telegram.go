// Package telegram implements the Telegram node (engi.chat.telegram) over the
// shared HTTP client: send and edit messages, send a document by URL and get
// a chat. The API base URL (nodes.chat.telegramBaseUrl) can point at a local
// Bot API server.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	credtelegram "github.com/engi-hq/engi/internal/credtype/telegram"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.chat.telegram"

// Operations.
const (
	opSend    = "sendMessage"
	opEdit    = "editMessage"
	opSendDoc = "sendDocument"
	opGetChat = "getChat"
)

// ErrTelegram is wrapped by every error the Bot API reports with ok:false.
var ErrTelegram = errors.New("telegram API error")

// Config is what the node needs from nodes.chat.*.
type Config struct {
	BaseURL string
	Caller  chatkit.Caller
}

// Node is the Telegram node.
type Node struct{ cfg Config }

// New returns a Telegram node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	text := nodekit.Show("operation", opSend, opEdit)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Telegram", Group: nodesdk.GroupAction, Icon: "send",
		Description: "Send and edit Telegram messages, send documents by URL and read chat details.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credtelegram.Name, Type: credtelegram.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opSend, Options: []nodesdk.Option{
				nodekit.Opt(opSend, "Send message"), nodekit.Opt(opEdit, "Edit message"),
				nodekit.Opt(opSendDoc, "Send document by URL"), nodekit.Opt(opGetChat, "Get chat")}},
			{Name: "chatId", DisplayName: "Chat ID", Type: nodesdk.PropString, Required: true, Description: "Numeric ID or @channelusername."},
			{Name: "messageId", DisplayName: "Message ID", Type: nodesdk.PropNumber, Required: true, DisplayOptions: nodekit.Show("operation", opEdit)},
			{Name: "text", DisplayName: "Text", Type: nodesdk.PropString, Required: true, DisplayOptions: text},
			{Name: "documentUrl", DisplayName: "Document URL", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opSendDoc)},
			{Name: "caption", DisplayName: "Caption", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opSendDoc)},
			{Name: "parseMode", DisplayName: "Parse mode", Type: nodesdk.PropOptions, Default: "", DisplayOptions: nodekit.Show("operation", opSend, opEdit, opSendDoc),
				Options: []nodesdk.Option{nodekit.Opt("", "Plain text"), nodekit.Opt("MarkdownV2", "MarkdownV2"), nodekit.Opt("HTML", "HTML")}},
			{Name: "disablePreview", DisplayName: "Disable link preview", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: text},
			{Name: "replyTo", DisplayName: "Reply to message ID", Type: nodesdk.PropNumber, Default: float64(0), DisplayOptions: nodekit.Show("operation", opSend, opSendDoc),
				Description: "0 sends a standalone message."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	cred, err := in.Credential(ctx, credtelegram.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credtelegram.Name, err)
	}
	c := &client{cfg: n.cfg, http: in.HTTP(), cred: cred, token: chatkit.Secret(cred, "token")}
	out := make([]nodesdk.Item, 0, len(items))
	for i := range items {
		method, params, err := buildCall(ctx, in, op, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		res, err := c.call(ctx, method, params)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func buildCall(ctx context.Context, in nodesdk.Input, op string, i int) (method string, p map[string]any, err error) {
	str := func(name string, required bool) (string, error) {
		v, err := nodesdk.ParamString(ctx, in, name, i)
		if err == nil && required && strings.TrimSpace(v) == "" {
			err = fmt.Errorf("%s must not be empty", name)
		}
		return v, err
	}
	chat, err := str("chatId", true)
	if err != nil {
		return "", nil, err
	}
	p = map[string]any{"chat_id": chat}
	set := func(key, param string, required bool) error {
		v, err := str(param, required)
		if err == nil && v != "" {
			p[key] = v
		}
		return err
	}
	mode := func() error { return set("parse_mode", "parseMode", false) }
	preview := func() error {
		off, err := nodesdk.ParamBool(ctx, in, "disablePreview", i)
		if off {
			p["link_preview_options"] = map[string]any{"is_disabled": true}
		}
		return err
	}
	reply := func() error {
		id, err := nodesdk.ParamInt(ctx, in, "replyTo", i)
		if id != 0 {
			p["reply_parameters"] = map[string]any{"message_id": id}
		}
		return err
	}
	var steps []func() error
	switch op {
	case opSend:
		method = "sendMessage"
		steps = []func() error{func() error { return set("text", "text", true) }, mode, preview, reply}
	case opEdit:
		method = "editMessageText"
		steps = []func() error{func() error { return set("text", "text", true) }, mode, preview, func() error {
			id, err := nodesdk.ParamInt(ctx, in, "messageId", i)
			if err == nil && id <= 0 {
				err = errors.New("messageId must be positive")
			}
			p["message_id"] = id
			return err
		}}
	case opSendDoc:
		method = "sendDocument"
		steps = []func() error{func() error { return set("document", "documentUrl", true) }, func() error { return set("caption", "caption", false) }, mode, reply}
	case opGetChat:
		method = "getChat"
	default:
		return "", nil, fmt.Errorf("unknown operation %q", op)
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return "", nil, err
		}
	}
	return method, p, nil
}

type client struct {
	cfg   Config
	http  nodesdk.HTTPClient
	cred  nodesdk.Credential
	token string
}

func (c *client) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encoding telegram %s: %w", method, err)
	}
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		u := strings.TrimRight(c.cfg.BaseURL, "/") + "/" + credtelegram.TokenSegment + "/" + method
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		return req, c.cred.Authenticate(req)
	}, c.token)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	var env struct {
		OK          bool   `json:"ok"`
		Result      any    `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("telegram %s: unexpected response (HTTP %d)", method, resp.Status), HTTPStatus: resp.Status}
	}
	if !env.OK {
		return nil, &nodesdk.NodeError{Message: fmt.Sprintf("telegram %s: %s", method, chatkit.Redact(env.Description, c.token)), HTTPStatus: resp.Status, Err: ErrTelegram}
	}
	if m, ok := env.Result.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{"result": env.Result}, nil
}
