// Package postgres elects a leader among replicas that share a Postgres
// database, with a session-level advisory lock held on a dedicated connection.
//
// The replica holding the lock leads. Its connection is checked every
// RetryPeriod; when the check fails (the connection or the database is gone)
// the replica cancels the leader context, waits for the callback to return,
// reports OnStoppedLeading, and goes back to campaigning on a new connection.
// The server releases the lock when the session ends, so a crashed replica is
// replaced without any timeout of ours. After a network partition the server
// may release the lock before the partitioned replica notices; the check
// bounds how long both can believe they lead to RenewDeadline plus the
// server's own dead-connection detection (TCP keepalive).
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// releaseTimeout bounds the explicit unlock on shutdown; closing the
// connection releases the lock anyway.
const releaseTimeout = 5 * time.Second

// Provider is a port.LeaseProvider on a Postgres advisory lock.
type Provider struct {
	target *pgdb.Target
	cfg    config.LeaseConfig
	log    *slog.Logger
}

var _ port.LeaseProvider = (*Provider)(nil)

// New returns a Provider campaigning for cfg.LockKey.
func New(target *pgdb.Target, cfg config.LeaseConfig, log *slog.Logger) *Provider {
	return &Provider{target: target, cfg: cfg, log: log}
}

// Run implements port.LeaseProvider. It returns nil once ctx is done and the
// lock is released.
func (p *Provider) Run(ctx context.Context, cb port.LeaderCallbacks) error {
	for ctx.Err() == nil {
		conn, err := p.target.Connect(ctx)
		if err != nil {
			p.log.Warn("lease: cannot connect to postgres", "error", err)
			p.sleep(ctx)
			continue
		}
		p.campaign(ctx, conn, cb)
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		_ = conn.Close(closeCtx)
		cancel()
		if ctx.Err() == nil {
			p.sleep(ctx)
		}
	}
	return nil
}

func (p *Provider) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(p.cfg.RetryPeriod):
	}
}

// campaign tries for the lock on conn and leads while it holds it, until ctx
// is done or the connection fails.
func (p *Provider) campaign(ctx context.Context, conn *pgx.Conn, cb port.LeaderCallbacks) {
	var lead *leadership
	defer func() {
		if lead != nil {
			lead.stop(cb)
			p.unlock(ctx, conn)
		}
	}()
	for {
		if lead == nil {
			ok, err := p.tryLock(ctx, conn)
			if err != nil {
				if ctx.Err() == nil {
					p.log.Warn("lease: lock attempt failed, reconnecting", "error", err)
				}
				return
			}
			if ok {
				lead = startLeading(ctx, cb)
			}
		} else if err := p.check(ctx, conn); err != nil {
			if ctx.Err() == nil {
				p.log.Warn("lease: connection check failed, stepping down", "error", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.cfg.RetryPeriod):
		}
	}
}

func (p *Provider) tryLock(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, p.cfg.LockKey).Scan(&ok); err != nil {
		return false, fmt.Errorf("trying advisory lock: %w", err)
	}
	return ok, nil
}

// check proves the session, and with it the lock, is alive: a session-level
// advisory lock lives exactly as long as its connection.
func (p *Provider) check(ctx context.Context, conn *pgx.Conn) error {
	cctx, cancel := context.WithTimeout(ctx, p.cfg.RenewDeadline)
	defer cancel()
	if err := conn.Ping(cctx); err != nil {
		return fmt.Errorf("pinging postgres: %w", err)
	}
	return nil
}

func (p *Provider) unlock(ctx context.Context, conn *pgx.Conn) {
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, p.cfg.LockKey); err != nil {
		p.log.Warn("lease: releasing the advisory lock failed (closing the connection releases it)", "error", err)
	}
}

// leadership is one tenure: the leader callback and its context.
type leadership struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func startLeading(ctx context.Context, cb port.LeaderCallbacks) *leadership {
	leaderCtx, cancel := context.WithCancel(ctx)
	l := &leadership{cancel: cancel}
	if cb.OnStartedLeading != nil {
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			cb.OnStartedLeading(leaderCtx)
		}()
	}
	return l
}

// stop cancels the leader context, waits for the callback and reports the end.
func (l *leadership) stop(cb port.LeaderCallbacks) {
	l.cancel()
	l.wg.Wait()
	if cb.OnStoppedLeading != nil {
		cb.OnStoppedLeading()
	}
}
