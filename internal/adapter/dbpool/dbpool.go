// Package dbpool implements port.DBPool: one database/sql pool per credential
// id and version for the Postgres (pgx) and MySQL drivers.
package dbpool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"time"

	"github.com/engi-hq/engi/internal/clientcache"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Config sizes every pool. The zero value is invalid; see config.DatabaseNodeConfig.
type Config struct {
	MaxOpenConns   int
	MaxIdleConns   int
	IdleTimeout    time.Duration
	MaxLifetime    time.Duration
	ConnectTimeout time.Duration
}

// ErrNoCredentialID is returned for a credential that cannot key a pool.
var ErrNoCredentialID = clientcache.ErrNoCredentialID

// ErrClosed is returned after Close.
var ErrClosed = clientcache.ErrClosed

// connectorFunc builds a driver connector from credential data.
type connectorFunc func(data map[string]any, connectTimeout time.Duration) (driver.Connector, error)

// Manager is the pool manager. It is safe for concurrent use.
type Manager struct {
	cfg        Config
	connectors map[string]connectorFunc
	pools      *clientcache.Cache[*sql.DB]
}

var _ port.DBPool = (*Manager)(nil)

// New returns a manager with no pools open.
func New(cfg Config, log *slog.Logger) *Manager {
	return &Manager{
		cfg: cfg, pools: clientcache.New(log, (*sql.DB).Close),
		connectors: map[string]connectorFunc{port.DriverPostgres: postgresConnector, port.DriverMySQL: mysqlConnector},
	}
}

// DB implements port.DBPool. Connections are opened lazily on first use, so a
// wrong host surfaces as a query error.
func (m *Manager) DB(ctx context.Context, drv string, cred nodesdk.Credential) (*sql.DB, error) {
	build, ok := m.connectors[drv]
	if !ok {
		return nil, fmt.Errorf("unsupported database driver %q", drv)
	}
	return m.pools.Get(ctx, drv, cred, func() (*sql.DB, error) {
		conn, err := build(cred.Data, m.cfg.ConnectTimeout)
		if err != nil {
			return nil, fmt.Errorf("building %s connection: %w", drv, err)
		}
		db := sql.OpenDB(conn)
		db.SetMaxOpenConns(m.cfg.MaxOpenConns)
		db.SetMaxIdleConns(m.cfg.MaxIdleConns)
		db.SetConnMaxIdleTime(m.cfg.IdleTimeout)
		db.SetConnMaxLifetime(m.cfg.MaxLifetime)
		return db, nil
	})
}

// Len reports the number of live pools.
func (m *Manager) Len() int { return m.pools.Len() }

// Close closes every pool and rejects further requests.
func (m *Manager) Close() error { return m.pools.Close() }
