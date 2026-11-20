package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// ChatMemory implements port.ChatMemoryStore.
type ChatMemory struct {
	s   *Store
	cfg config.AIMemoryConfig
}

var _ port.ChatMemoryStore = ChatMemory{}

// ChatMemory returns the chat memory store bounded by cfg.
func (s *Store) ChatMemory(cfg config.AIMemoryConfig) ChatMemory { return ChatMemory{s: s, cfg: cfg} }

// Load returns the last limit messages of the session, oldest first.
func (c ChatMemory) Load(ctx context.Context, session string, limit int) ([]port.LLMMessage, error) {
	rows, err := c.s.run.Q(ctx).QueryContext(ctx,
		`SELECT role, content, tool_calls, tool_call_id, is_error FROM chat_messages
		 WHERE session_key = ? ORDER BY id DESC LIMIT ?`, session, limit)
	if err != nil {
		return nil, fmt.Errorf("loading chat messages: %w", err)
	}
	defer rows.Close()
	var out []port.LLMMessage
	for rows.Next() {
		var m port.LLMMessage
		var role, calls string
		if err := rows.Scan(&role, &m.Content, &calls, &m.ToolCallID, &m.IsError); err != nil {
			return nil, fmt.Errorf("scanning chat message: %w", err)
		}
		m.Role = port.LLMRole(role)
		if m.ToolCalls, err = jsoncodec.DecodeToolCalls(calls); err != nil {
			return nil, err //nolint:wrapcheck // codec errors are descriptive
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading chat messages: %w", err)
	}
	slices.Reverse(out)
	return out, nil
}

// Append adds messages, then enforces the per-session cap and the retention.
func (c ChatMemory) Append(ctx context.Context, session string, msgs []port.LLMMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	now := c.s.clk.Now()
	return c.s.WithinTx(ctx, func(ctx context.Context) error {
		q := c.s.run.Q(ctx)
		for _, m := range msgs {
			calls, err := jsoncodec.EncodeToolCalls(m.ToolCalls)
			if err != nil {
				return err //nolint:wrapcheck // codec errors are descriptive
			}
			if _, err := q.ExecContext(ctx,
				`INSERT INTO chat_messages (session_key, role, content, tool_calls, tool_call_id, is_error, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				session, string(m.Role), m.Content, calls, m.ToolCallID, m.IsError, pgdb.Stamp(now)); err != nil {
				return fmt.Errorf("storing chat message: %w", err)
			}
		}
		if _, err := q.ExecContext(ctx,
			`DELETE FROM chat_messages WHERE session_key = ? AND id NOT IN
			 (SELECT id FROM chat_messages WHERE session_key = ? ORDER BY id DESC LIMIT ?)`,
			session, session, c.cfg.MaxMessages); err != nil {
			return fmt.Errorf("trimming chat session: %w", err)
		}
		return c.prune(ctx, now)
	})
}

func (c ChatMemory) prune(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-c.cfg.Retention)
	if _, err := c.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM chat_messages WHERE created_at < ?`, pgdb.Stamp(cutoff)); err != nil {
		return fmt.Errorf("pruning chat messages: %w", err)
	}
	return nil
}

// Clear deletes the session.
func (c ChatMemory) Clear(ctx context.Context, session string) error {
	if _, err := c.s.run.Q(ctx).ExecContext(ctx, `DELETE FROM chat_messages WHERE session_key = ?`, session); err != nil {
		return fmt.Errorf("clearing chat session: %w", err)
	}
	return nil
}
