// Package port declares the interfaces the core uses to reach the outside
// world. Adapters implement them; services consume them.
package port

import (
	"context"
	"time"
)

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Sleeper pauses execution, honouring cancellation. It is separate from Clock
// so tests can run retries and backoff without real delays.
type Sleeper interface {
	// Sleep blocks for d or until ctx is done, returning ctx.Err() in that case.
	Sleep(ctx context.Context, d time.Duration) error
}
