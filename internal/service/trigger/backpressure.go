package trigger

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/port"
)

// gate tells listeners to stop taking new work while the job queue is deep. It
// pauses above one depth and resumes below a lower one, so the state does not
// flap around a single threshold. The depth is sampled at most once per
// interval, however many listeners ask.
type gate struct {
	metrics  port.Metrics
	stats    port.QueueStats
	clock    port.Clock
	pause    int
	resume   int
	interval time.Duration
	log      *slog.Logger

	mu         sync.Mutex
	overloaded bool
	sampled    time.Time
}

// isOverloaded reports the current state, sampling the queue when due. A failed
// sample keeps the previous state.
func (g *gate) isOverloaded(ctx context.Context) bool {
	if g.stats == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	if !g.sampled.IsZero() && now.Sub(g.sampled) < g.interval {
		return g.overloaded
	}
	depth, err := g.stats.Depth(ctx)
	if err != nil {
		if ctx.Err() == nil {
			g.log.Warn("sampling the job queue depth failed", "error", err)
		}
		return g.overloaded
	}
	g.sampled = now
	g.overloaded = nextOverloaded(g.overloaded, depth, g.pause, g.resume)
	g.metrics.KafkaPaused(g.overloaded)
	return g.overloaded
}

// nextOverloaded applies the hysteresis rule to one depth sample.
func nextOverloaded(current bool, depth, pause, resume int) bool {
	switch {
	case !current && depth > pause:
		return true
	case current && depth < resume:
		return false
	}
	return current
}
