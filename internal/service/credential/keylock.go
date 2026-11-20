package credential

import (
	"context"
	"fmt"
	"sync"
)

// keyLock is a mutex per key that honours context cancellation while waiting.
// Entries are removed when unused.
type keyLock struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
}

type lockEntry struct {
	ch   chan struct{}
	refs int
}

func newKeyLock() *keyLock { return &keyLock{entries: map[string]*lockEntry{}} }

// lock acquires the lock for key and returns the release function.
func (k *keyLock) lock(ctx context.Context, key string) (func(), error) {
	k.mu.Lock()
	e := k.entries[key]
	if e == nil {
		e = &lockEntry{ch: make(chan struct{}, 1)}
		k.entries[key] = e
	}
	e.refs++
	k.mu.Unlock()

	release := func() {
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
	select {
	case e.ch <- struct{}{}:
		return func() { <-e.ch; release() }, nil
	case <-ctx.Done():
		release()
		return nil, fmt.Errorf("waiting for credential lock: %w", ctx.Err())
	}
}
