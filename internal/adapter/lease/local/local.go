// Package local is the leader election of a standalone process: it is always
// the leader. Cluster adapters (Kubernetes Lease, Postgres advisory lock)
// implement the same port.LeaseProvider.
package local

import (
	"context"
	"sync"

	"github.com/engi-hq/engi/internal/port"
)

// Provider makes the process the leader for as long as Run is running.
type Provider struct{}

var _ port.LeaseProvider = Provider{}

// New returns a local provider.
func New() Provider { return Provider{} }

// Run starts leading at once and stops when ctx is done. It waits for the
// leader callback to return before it calls OnStoppedLeading.
func (Provider) Run(ctx context.Context, cb port.LeaderCallbacks) error {
	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	if cb.OnStartedLeading != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.OnStartedLeading(leaderCtx)
		}()
	}
	<-ctx.Done()
	cancel()
	wg.Wait()
	if cb.OnStoppedLeading != nil {
		cb.OnStoppedLeading()
	}
	return nil
}
