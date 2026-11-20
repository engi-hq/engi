// Package system provides the real-time Clock and Sleeper.
package system

import (
	"context"
	"fmt"
	"time"
)

// Clock reads the wall clock and sleeps for real.
type Clock struct{}

// Now returns the current time.
func (Clock) Now() time.Time { return time.Now() }

// Sleep waits for d or until ctx is done.
func (Clock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("sleep interrupted: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}
