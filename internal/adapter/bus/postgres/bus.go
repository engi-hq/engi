// Package postgres implements port.Bus over Postgres LISTEN/NOTIFY, so events
// published by one pod reach the subscribers of every pod (a cancel requested
// on the API pod stops the execution running on a worker pod).
//
// Delivery stays best-effort, as the port promises: a subscriber that cannot
// keep up loses events, and events published while a pod's listener connection
// is down are lost to that pod. Durable facts live in the stores.
//
// A NOTIFY payload is limited to just under 8000 bytes. Events up to
// BusConfig.MaxInlineBytes travel inline; larger ones (a node run with big
// items) are stored in the bus_events table and only their id is notified.
// Stored events are pruned after BusConfig.EventRetention.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const (
	channel       = "engi_events"
	inlinePrefix  = "i:"
	storedPrefix  = "r:"
	runKey        = "run"
	idBase        = 10
	idBits        = 64
	logDropFormat = "event bus subscriber is too slow, dropping event"
	// loadTimeout bounds reading a stored large event.
	loadTimeout = 5 * time.Second
)

// Bus is a cluster-wide port.Bus.
type Bus struct {
	db       *sql.DB
	target   *pgdb.Target
	cfg      config.BusConfig
	buffer   int
	delay    time.Duration
	clk      port.Clock
	log      *slog.Logger
	listener *pgdb.Listener

	mu     sync.Mutex
	nextID int
	subs   map[int]*subscription
}

var _ port.Bus = (*Bus)(nil)

// New returns a Bus. Subscribers buffer up to buffer events; reconnectDelay is
// the pause before a lost listener connection is re-established. Call Start
// before subscribing.
func New(db *sql.DB, target *pgdb.Target, cfg config.BusConfig, buffer int, reconnectDelay time.Duration, clk port.Clock, log *slog.Logger) (*Bus, error) {
	if buffer <= 0 {
		return nil, errors.New("postgres bus: buffer must be greater than zero")
	}
	return &Bus{db: db, target: target, cfg: cfg, buffer: buffer, delay: reconnectDelay, clk: clk, log: log, subs: map[int]*subscription{}}, nil
}

// Start opens the dedicated listener connection and returns once it is
// listening. It fails when the first connection cannot be made; later drops
// are retried in the background. Close stops it.
func (b *Bus) Start(ctx context.Context) error {
	b.listener = pgdb.NewListener(b.target, []string{channel}, b.delay, b.log, b.onNotify, nil)
	if err := b.listener.Start(ctx); err != nil {
		return fmt.Errorf("starting event bus listener: %w", err)
	}
	return nil
}

// Close stops the listener and ends every subscription.
func (b *Bus) Close() {
	if b.listener != nil {
		b.listener.Stop()
	}
	b.mu.Lock()
	subs := make([]*subscription, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}

type wireEvent struct {
	Kind        port.EventKind  `json:"kind"`
	ExecutionID string          `json:"executionId,omitempty"`
	WorkflowID  string          `json:"workflowId,omitempty"`
	At          time.Time       `json:"at"`
	Node        string          `json:"node,omitempty"`
	RunIndex    int             `json:"runIndex,omitempty"`
	Run         json.RawMessage `json:"run,omitempty"`
	Status      string          `json:"status,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
}

func encode(e port.Event) ([]byte, error) {
	w := wireEvent{
		Kind: e.Kind, ExecutionID: string(e.ExecutionID), WorkflowID: string(e.WorkflowID), At: e.At,
		Node: e.Node, RunIndex: e.RunIndex, Status: string(e.Status),
	}
	if e.Run != nil {
		b, err := jsoncodec.EncodeRunData(map[string][]domain.NodeRun{runKey: {*e.Run}})
		if err != nil {
			return nil, fmt.Errorf("encoding node run: %w", err)
		}
		w.Run = b
	}
	if e.Error != nil {
		b, err := jsoncodec.EncodeErrorInfo(e.Error)
		if err != nil {
			return nil, fmt.Errorf("encoding error: %w", err)
		}
		w.Error = b
	}
	out, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encoding event: %w", err)
	}
	return out, nil
}

func decode(data []byte) (port.Event, error) {
	var w wireEvent
	if err := json.Unmarshal(data, &w); err != nil {
		return port.Event{}, fmt.Errorf("decoding event: %w", err)
	}
	e := port.Event{
		Kind: w.Kind, ExecutionID: domain.ExecutionID(w.ExecutionID), WorkflowID: domain.WorkflowID(w.WorkflowID), At: w.At,
		Node: w.Node, RunIndex: w.RunIndex, Status: domain.ExecutionStatus(w.Status),
	}
	if len(w.Run) > 0 {
		runs, err := jsoncodec.DecodeRunData(w.Run)
		if err != nil {
			return port.Event{}, fmt.Errorf("decoding node run: %w", err)
		}
		if rs := runs[runKey]; len(rs) == 1 {
			e.Run = &rs[0]
		}
	}
	if len(w.Error) > 0 {
		ei, err := jsoncodec.DecodeErrorInfo(w.Error)
		if err != nil {
			return port.Event{}, fmt.Errorf("decoding error: %w", err)
		}
		e.Error = ei
	}
	return e, nil
}

// Publish implements port.Bus. It does not use a transaction carried by ctx:
// an event announces something already decided.
func (b *Bus) Publish(ctx context.Context, e port.Event) error {
	body, err := encode(e)
	if err != nil {
		return err
	}
	payload := inlinePrefix + string(body)
	if len(body) > b.cfg.MaxInlineBytes {
		id, err := b.store(ctx, body)
		if err != nil {
			return err
		}
		payload = storedPrefix + strconv.FormatInt(id, idBase)
	}
	if _, err := b.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		return fmt.Errorf("publishing event: %w", err)
	}
	return nil
}

// store saves a large event and prunes expired ones.
func (b *Bus) store(ctx context.Context, body []byte) (int64, error) {
	now := b.clk.Now()
	var id int64
	err := b.db.QueryRowContext(ctx,
		`INSERT INTO bus_events (payload, created_at) VALUES ($1, $2) RETURNING id`, string(body), pgdb.Stamp(now)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storing large event: %w", err)
	}
	if _, err := b.db.ExecContext(ctx, `DELETE FROM bus_events WHERE created_at < $1`, pgdb.Stamp(now.Add(-b.cfg.EventRetention))); err != nil {
		b.log.Warn("pruning stored bus events failed", "error", err)
	}
	return id, nil
}

// onNotify runs on the listener goroutine for every notification.
func (b *Bus) onNotify(_, payload string) {
	if !b.hasSubscribers() {
		return
	}
	var body []byte
	switch {
	case strings.HasPrefix(payload, inlinePrefix):
		body = []byte(strings.TrimPrefix(payload, inlinePrefix))
	case strings.HasPrefix(payload, storedPrefix):
		id, err := strconv.ParseInt(strings.TrimPrefix(payload, storedPrefix), idBase, idBits)
		if err != nil {
			b.log.Warn("event bus: bad stored event reference", "payload", payload)
			return
		}
		loaded, err := b.load(id)
		if err != nil {
			b.log.Warn("event bus: cannot load stored event", "id", id, "error", err)
			return
		}
		body = loaded
	default:
		b.log.Warn("event bus: unknown payload format")
		return
	}
	e, err := decode(body)
	if err != nil {
		b.log.Warn("event bus: dropping undecodable event", "error", err)
		return
	}
	b.deliver(e)
}

func (b *Bus) load(id int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	var payload string
	if err := b.db.QueryRowContext(ctx, `SELECT payload FROM bus_events WHERE id = $1`, id).Scan(&payload); err != nil {
		return nil, fmt.Errorf("reading stored event: %w", err)
	}
	return []byte(payload), nil
}

func (b *Bus) hasSubscribers() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs) > 0
}

func (b *Bus) deliver(e port.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if !s.filter.Matches(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			b.log.Warn(logDropFormat, "kind", e.Kind, "execution", e.ExecutionID)
		}
	}
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
// bus lock, so delivery never sends on a closed channel.
func (s *subscription) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subs, s.id)
		close(s.ch)
		s.bus.mu.Unlock()
	})
}
