package port

import "context"

// ChatMemoryStore persists conversation messages per session so an agent can
// continue a chat across executions. A session is identified by an opaque key
// the memory node builds (workflow plus session id). Implementations must be
// safe for concurrent use and keep messages in append order.
//
// The store enforces the configured limits itself (messages per session,
// retention); callers never prune.
type ChatMemoryStore interface {
	// Load returns the last limit messages of the session, oldest first. An
	// unknown session returns an empty slice. limit must be positive.
	Load(ctx context.Context, session string, limit int) ([]LLMMessage, error)
	// Append adds messages to the end of the session atomically.
	Append(ctx context.Context, session string, msgs []LLMMessage) error
	// Clear deletes every message of the session. An unknown session is not
	// an error.
	Clear(ctx context.Context, session string) error
}

// ChatMemory is a conversation memory already bound to one session: the value
// an ai.memory sub-node supplies to its parent (the agent).
type ChatMemory interface {
	// History returns the messages to prepend to a new request, oldest first.
	// The window never starts in the middle of a tool exchange, so it is safe
	// to send as is.
	History(ctx context.Context) ([]LLMMessage, error)
	// Save appends the messages of a finished exchange.
	Save(ctx context.Context, msgs []LLMMessage) error
	// Clear forgets the session.
	Clear(ctx context.Context) error
}
