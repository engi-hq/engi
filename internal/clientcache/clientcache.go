// Package clientcache keeps one live client per key and credential version.
// The database pool, Redis and MongoDB managers are thin typed wrappers over it.
package clientcache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ErrNoCredentialID is returned for a credential that cannot key a client.
var ErrNoCredentialID = errors.New("credential has no id; cannot pool its connections")

// ErrClosed is returned after Close.
var ErrClosed = errors.New("client manager is closed")

type entry[C any] struct {
	version int64
	client  C
}

// Cache is safe for concurrent use. Editing a credential (a new version)
// closes the old client and builds a new one on the next Get.
type Cache[C any] struct {
	log     *slog.Logger
	closeFn func(C) error

	mu      sync.Mutex
	entries map[string]entry[C]
	closed  bool
}

// New returns an empty cache. closeFn releases one client.
func New[C any](log *slog.Logger, closeFn func(C) error) *Cache[C] {
	return &Cache[C]{log: log, closeFn: closeFn, entries: map[string]entry[C]{}}
}

// Get returns the client for key at the credential's version, calling build
// when there is none (or the version changed).
func (c *Cache[C]) Get(ctx context.Context, key string, cred nodesdk.Credential, build func() (C, error)) (C, error) {
	var zero C
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("getting client: %w", err)
	}
	if cred.ID == "" {
		return zero, ErrNoCredentialID
	}
	key += "/" + cred.ID
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return zero, ErrClosed
	}
	if e, ok := c.entries[key]; ok {
		if e.version == cred.Version {
			return e.client, nil
		}
		delete(c.entries, key)
		if err := c.closeFn(e.client); err != nil {
			c.log.Warn("closing client", "key", key, "reason", "credential changed", "error", err)
		}
	}
	client, err := build()
	if err != nil {
		return zero, err
	}
	c.entries[key] = entry[C]{version: cred.Version, client: client}
	return client, nil
}

// Len reports the number of live clients.
func (c *Cache[C]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close closes every client and rejects further requests.
func (c *Cache[C]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var errs []error
	for k, e := range c.entries {
		delete(c.entries, k)
		if err := c.closeFn(e.client); err != nil {
			errs = append(errs, fmt.Errorf("closing client %s: %w", k, err))
		}
	}
	return errors.Join(errs...)
}
