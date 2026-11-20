package auth

import (
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// loginLimiter remembers recent failed logins per key (an account or a client
// address) in memory. A key is blocked while it has maxFailures failures
// inside the window. State is per process: in a cluster each replica counts
// on its own, which only makes the limit looser, never stricter.
type loginLimiter struct {
	cfg   config.LoginLimitConfig
	clock port.Clock

	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginLimiter(cfg config.LoginLimitConfig, clock port.Clock) *loginLimiter {
	return &loginLimiter{cfg: cfg, clock: clock, failures: map[string][]time.Time{}}
}

// check returns a *domain.RateLimitedError when the key is blocked.
func (l *loginLimiter) check(key string, limit int) error {
	if !l.cfg.Enabled {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	recent := l.prune(key, now)
	if len(recent) < limit {
		return nil
	}
	// Blocked until enough old failures leave the window to drop below limit.
	until := recent[len(recent)-limit].Add(l.cfg.Window)
	return &domain.RateLimitedError{RetryAfter: until.Sub(now)}
}

// fail records a failed attempt.
func (l *loginLimiter) fail(key string) {
	if !l.cfg.Enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if _, ok := l.failures[key]; !ok && len(l.failures) >= l.cfg.MaxTrackedKeys {
		l.sweep(now)
		if len(l.failures) >= l.cfg.MaxTrackedKeys {
			return // full of live entries: skip tracking rather than grow without bound
		}
	}
	l.failures[key] = append(l.prune(key, now), now)
}

// reset forgets the failures of a key after a successful login.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// prune drops failures older than the window and returns the rest. Callers hold mu.
func (l *loginLimiter) prune(key string, now time.Time) []time.Time {
	list := l.failures[key]
	cut := 0
	for cut < len(list) && !list[cut].After(now.Add(-l.cfg.Window)) {
		cut++
	}
	list = list[cut:]
	if len(list) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = list
	return list
}

// sweep removes every key without a failure inside the window. Callers hold mu.
func (l *loginLimiter) sweep(now time.Time) {
	for key := range l.failures {
		l.prune(key, now)
	}
}
