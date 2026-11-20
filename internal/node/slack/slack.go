// Package slack implements the Slack node (engi.chat.slack) over the shared
// HTTP client: post, update and delete messages, upload a text file, list
// channels and look a user up by email.
package slack

import (
	"context"
	"fmt"

	credslack "github.com/engi-hq/engi/internal/credtype/slack"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.chat.slack"

// Operations.
const (
	opPost     = "postMessage"
	opUpdate   = "updateMessage"
	opDelete   = "deleteMessage"
	opUpload   = "uploadFile"
	opChannels = "listChannels"
	opUser     = "getUserByEmail"
)

// Config is what the node needs from nodes.chat.*.
type Config struct {
	BaseURL        string
	MaxListResults int
	PageSize       int
	Caller         chatkit.Caller
}

// Node is the Slack node.
type Node struct{ cfg Config }

// New returns a Slack node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := [][2]string{
		{opPost, "Post message"}, {opUpdate, "Update message"}, {opDelete, "Delete message"},
		{opUpload, "Upload text file"}, {opChannels, "List channels"}, {opUser, "Get user by email"},
	}
	opts := make([]nodesdk.Option, len(ops))
	for i, o := range ops {
		opts[i] = nodekit.Opt(o[0], o[1])
	}
	msg := nodekit.Show("operation", opPost, opUpdate)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Slack", Group: nodesdk.GroupAction, Icon: "message-square",
		Description: "Post, update and delete Slack messages, upload text files, list channels and find users.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credslack.Name, Type: credslack.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opPost, Options: opts},
			{Name: "channel", DisplayName: "Channel", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opPost, opUpdate, opDelete, opUpload),
				Description: "Channel ID (C0123...) or, for posting, a channel name."},
			{Name: "text", DisplayName: "Text", Type: nodesdk.PropString, DisplayOptions: msg},
			{Name: "blocks", DisplayName: "Blocks (JSON)", Type: nodesdk.PropJSON, DisplayOptions: msg, Description: "Optional Block Kit array."},
			{Name: "threadTs", DisplayName: "Thread timestamp", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opPost, opUpload),
				Description: "Reply in the thread of this message."},
			{Name: "ts", DisplayName: "Message timestamp", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opUpdate, opDelete)},
			{Name: "content", DisplayName: "File content", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opUpload)},
			{Name: "filename", DisplayName: "File name", Type: nodesdk.PropString, Default: "file.txt", DisplayOptions: nodekit.Show("operation", opUpload)},
			{Name: "title", DisplayName: "Title", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opUpload)},
			{Name: "comment", DisplayName: "Comment", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opUpload)},
			{Name: "types", DisplayName: "Channel types", Type: nodesdk.PropString, Default: "public_channel", DisplayOptions: nodekit.Show("operation", opChannels),
				Description: "Comma separated: public_channel, private_channel, mpim, im."},
			{Name: "excludeArchived", DisplayName: "Exclude archived", Type: nodesdk.PropBoolean, Default: true, DisplayOptions: nodekit.Show("operation", opChannels)},
			{Name: "email", DisplayName: "Email", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opUser)},
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
	cred, err := in.Credential(ctx, credslack.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credslack.Name, err)
	}
	c := &client{cfg: n.cfg, http: in.HTTP(), cred: cred, token: chatkit.Secret(cred, "token")}
	out := make([]nodesdk.Item, 0, len(items))
	for i := range items {
		res, err := c.run(ctx, in, op, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
