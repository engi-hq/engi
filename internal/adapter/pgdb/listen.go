package pgdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotifyFunc receives one notification.
type NotifyFunc func(channel, payload string)

// Listener keeps a dedicated connection LISTENing on channels and reconnects
// when it drops. After every (re)connect it calls onConnect so subscribers can
// catch up on notifications missed meanwhile.
type Listener struct {
	target    *Target
	channels  []string
	delay     time.Duration
	log       *slog.Logger
	onNotify  NotifyFunc
	onConnect func()

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewListener returns a Listener for channels. delay is the pause between
// reconnect attempts. onConnect may be nil.
func NewListener(t *Target, channels []string, delay time.Duration, log *slog.Logger, onNotify NotifyFunc, onConnect func()) *Listener {
	return &Listener{target: t, channels: channels, delay: delay, log: log, onNotify: onNotify, onConnect: onConnect}
}

// Start connects once (returning the error if that fails) and then serves
// notifications in a goroutine until Stop is called or ctx is done.
func (l *Listener) Start(ctx context.Context) error {
	conn, err := l.connect(ctx)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	l.mu.Lock()
	l.cancel, l.done = cancel, done
	l.mu.Unlock()
	stop := context.AfterFunc(ctx, cancel)
	go func() {
		defer close(done)
		defer stop()
		l.run(runCtx, conn)
	}()
	return nil
}

// Stop ends the goroutine and closes the connection. It is safe to call twice.
func (l *Listener) Stop() {
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (l *Listener) connect(ctx context.Context) (*pgx.Conn, error) {
	conn, err := l.target.Connect(ctx)
	if err != nil {
		return nil, err
	}
	for _, ch := range l.channels {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			_ = conn.Close(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("listening on %s: %w", ch, err)
		}
	}
	if l.onConnect != nil {
		l.onConnect()
	}
	return conn, nil
}

func (l *Listener) run(ctx context.Context, conn *pgx.Conn) {
	for {
		err := l.wait(ctx, conn)
		_ = conn.Close(context.WithoutCancel(ctx))
		if ctx.Err() != nil {
			return
		}
		l.log.Warn("postgres listener lost its connection, reconnecting", "error", err)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(l.delay):
			}
			var cerr error
			if conn, cerr = l.connect(ctx); cerr == nil {
				break
			}
			l.log.Warn("postgres listener cannot reconnect", "error", cerr)
		}
	}
}

func (l *Listener) wait(ctx context.Context, conn *pgx.Conn) error {
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("waiting for notification: %w", err)
		}
		l.onNotify(n.Channel, n.Payload)
	}
}
