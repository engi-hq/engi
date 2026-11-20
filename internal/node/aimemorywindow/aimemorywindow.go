// Package aimemorywindow implements the Window Memory sub-node
// (engi.ai.memory.window): it gives the connected AI Agent the last N messages
// of a chat session and stores the new exchange, backed by port.ChatMemoryStore.
package aimemorywindow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.memory.window"

// DefaultSessionKey is the default session key expression: the sessionId a
// chat trigger puts on its item.
const DefaultSessionKey = "={{ $json.sessionId }}"

// Config is what the node needs from the host.
type Config struct {
	// Store persists the messages. Nil makes Supply fail with
	// nodesdk.ErrUnavailable.
	Store port.ChatMemoryStore
	// DefaultWindow is the window used when the node sets none.
	DefaultWindow int
	// MaxWindow caps the window a node may ask for.
	MaxWindow int
}

// Node is the Window Memory sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns a Window Memory node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Window Memory", Group: nodesdk.GroupAI, Icon: "database",
		Description: "Remembers the last messages of a chat session for an AI Agent.",
		Outputs:     []nodesdk.PortSpec{{Name: "memory", Kind: nodesdk.PortAIMemory, DisplayName: "Memory"}},
		Properties: []nodesdk.Property{
			{Name: "sessionKey", DisplayName: "Session key", Type: nodesdk.PropString, Required: true, Default: DefaultSessionKey,
				Description: "Identifies the conversation. Messages are stored per workflow and session key. The chat trigger supplies sessionId on its item; use an expression for anything else (for example a user id)."},
			{Name: "windowSize", DisplayName: "Window size", Type: nodesdk.PropNumber,
				Description: "How many of the latest messages the agent sees. Empty uses nodes.ai.memory.defaultWindow."},
		},
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("window memory: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ChatMemory bound to the
// session of the parent's current item.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	if n.cfg.Store == nil {
		return nil, fmt.Errorf("chat memory store: %w", nodesdk.ErrUnavailable)
	}
	session, err := nodesdk.ParamString(ctx, in, "sessionKey", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if session = strings.TrimSpace(session); session == "" {
		return nil, errors.New("session key is empty: the item has no sessionId (a chat trigger sets it); set the session key explicitly")
	}
	window, err := nodesdk.ParamInt(ctx, in, "windowSize", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // SDK errors are descriptive
	}
	switch {
	case window < 0:
		return nil, errors.New("window size must not be negative")
	case window == 0:
		window = n.cfg.DefaultWindow
	case window > n.cfg.MaxWindow:
		return nil, fmt.Errorf("window size %d exceeds the limit %d (nodes.ai.memory.maxWindow)", window, n.cfg.MaxWindow)
	}
	return &memory{store: n.cfg.Store, key: StoreKey(in.ExecutionInfo().WorkflowID, session), window: window}, nil
}

// StoreKey scopes a session to a workflow so equal session ids in different
// workflows never share messages. The length prefix keeps the pair unambiguous.
func StoreKey(workflowID, session string) string {
	return fmt.Sprintf("%d:%s:%s", len(workflowID), workflowID, session)
}

type memory struct {
	store  port.ChatMemoryStore
	key    string
	window int
}

var _ port.ChatMemory = (*memory)(nil)

// History implements port.ChatMemory.
func (m *memory) History(ctx context.Context) ([]port.LLMMessage, error) {
	msgs, err := m.store.Load(ctx, m.key, m.window)
	if err != nil {
		return nil, fmt.Errorf("loading chat history: %w", err)
	}
	return startAtUser(msgs), nil
}

// startAtUser drops leading messages until the first user message, so the
// window never begins inside a tool exchange or with an assistant turn (which
// providers reject).
func startAtUser(msgs []port.LLMMessage) []port.LLMMessage {
	for i, m := range msgs {
		if m.Role == port.RoleUser {
			return msgs[i:]
		}
	}
	return nil
}

// Save implements port.ChatMemory.
func (m *memory) Save(ctx context.Context, msgs []port.LLMMessage) error {
	if err := m.store.Append(ctx, m.key, msgs); err != nil {
		return fmt.Errorf("saving chat messages: %w", err)
	}
	return nil
}

// Clear implements port.ChatMemory.
func (m *memory) Clear(ctx context.Context) error {
	if err := m.store.Clear(ctx, m.key); err != nil {
		return fmt.Errorf("clearing chat session: %w", err)
	}
	return nil
}
