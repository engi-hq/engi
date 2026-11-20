// Package memory implements port.Bus inside one process. It is the bus of the
// standalone binary; cluster deployments use a networked adapter.
package memory

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/engi-hq/engi/internal/port"
)

// Bus is an in-process port.Bus. Each subscriber has a bounded buffer; when it
// is full the event is dropped for that subscriber and counted.
type Bus struct {
	buffer int
	log    *slog.Logger

	mu     sync.Mutex
	nextID int
	subs   map[int]*subscription
}

var _ port.Bus = (*Bus)(nil)

// New returns a Bus whose subscribers buffer up to buffer events.
func New(buffer int, log *slog.Logger) (*Bus, error) {
	if buffer <= 0 {
		return nil, errors.New("memory bus: buffer must be greater than zero")
	}
	return &Bus{buffer: buffer, log: log, subs: map[int]*subscription{}}, nil
}

// Publish implements port.Bus. It never blocks.
func (b *Bus) Publish(_ context.Context, e port.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if !s.filter.Matches(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.log.Warn("event bus subscriber is too slow, dropping event", "kind", e.Kind, "execution", e.ExecutionID)
		}
	}
	return nil
}

// Subscribe implements port.Bus. The subscription ends when ctx is done.
func (b *Bus) Subscribe(ctx context.Context, f port.EventFilter) (port.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err //nolint:wrapcheck // the caller's own context error
	}
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	s := &subscription{bus: b, id: id, filter: f, ch: make(chan port.Event, b.buffer)}
	b.subs[id] = s
	b.mu.Unlock()
	context.AfterFunc(ctx, s.Close)
	return s, nil
}

type subscription struct {
	bus    *Bus
	id     int
	filter port.EventFilter
	ch     chan port.Event
	once   sync.Once
}

// Events implements port.Subscription.
func (s *subscription) Events() <-chan port.Event { return s.ch }

// Close implements port.Subscription. Closing the channel happens under the
// bus lock, so Publish never sends on a closed channel.
func (s *subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s.id)
		close(s.ch)
		s.bus.mu.Unlock()
	})
}
