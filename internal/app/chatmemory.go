package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// chatMemoryBridge hands the memory node a store before the runtime (which
// owns the database) exists, like subworkflowBridge. Until the runtime points
// it at the real store, every call fails with nodesdk.ErrUnavailable, which is
// what `engi run` without --persist gets.
type chatMemoryBridge struct {
	target atomic.Pointer[port.ChatMemoryStore]
}

var _ port.ChatMemoryStore = (*chatMemoryBridge)(nil)

func (b *chatMemoryBridge) set(s port.ChatMemoryStore) { b.target.Store(&s) }

func (b *chatMemoryBridge) store() (port.ChatMemoryStore, error) {
	t := b.target.Load()
	if t == nil {
		return nil, fmt.Errorf("chat memory: %w (run with a persistent runtime, e.g. engi run --persist)", nodesdk.ErrUnavailable)
	}
	return *t, nil
}

// Load implements port.ChatMemoryStore.
func (b *chatMemoryBridge) Load(ctx context.Context, session string, limit int) ([]port.LLMMessage, error) {
	s, err := b.store()
	if err != nil {
		return nil, err
	}
	return s.Load(ctx, session, limit) //nolint:wrapcheck // the real store's errors are descriptive
}

// Append implements port.ChatMemoryStore.
func (b *chatMemoryBridge) Append(ctx context.Context, session string, msgs []port.LLMMessage) error {
	s, err := b.store()
	if err != nil {
		return err
	}
	return s.Append(ctx, session, msgs) //nolint:wrapcheck // the real store's errors are descriptive
}

// Clear implements port.ChatMemoryStore.
func (b *chatMemoryBridge) Clear(ctx context.Context, session string) error {
	s, err := b.store()
	if err != nil {
		return err
	}
	return s.Clear(ctx, session) //nolint:wrapcheck // the real store's errors are descriptive
}
