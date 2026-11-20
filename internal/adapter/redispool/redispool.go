// Package redispool hands out one go-redis client per credential id and version.
package redispool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/engi-hq/engi/internal/clientcache"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Credential defaults mirror the credential type's field defaults.
const (
	defaultHost = "localhost"
	defaultPort = 6379
)

// Config sizes every client. See config.DatabaseNodeConfig.
type Config struct {
	PoolSize       int
	IdleTimeout    time.Duration
	MaxLifetime    time.Duration
	ConnectTimeout time.Duration
}

// Manager is safe for concurrent use.
type Manager struct {
	cfg   Config
	cache *clientcache.Cache[*redis.Client]
}

// New returns a manager with no clients open.
func New(cfg Config, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, cache: clientcache.New(log, (*redis.Client).Close)}
}

// Redis returns the shared client for the credential. Connections are opened
// lazily, so a wrong host surfaces as a command error.
func (m *Manager) Redis(ctx context.Context, cred nodesdk.Credential) (*redis.Client, error) {
	return m.cache.Get(ctx, "redis", cred, func() (*redis.Client, error) {
		opts, err := Options(cred.Data)
		if err != nil {
			return nil, fmt.Errorf("building redis connection: %w", err)
		}
		opts.PoolSize = m.cfg.PoolSize
		opts.ConnMaxIdleTime = m.cfg.IdleTimeout
		opts.ConnMaxLifetime = m.cfg.MaxLifetime
		opts.DialTimeout = m.cfg.ConnectTimeout
		return redis.NewClient(opts), nil
	})
}

// Len reports the number of live clients.
func (m *Manager) Len() int { return m.cache.Len() }

// Close closes every client and rejects further requests.
func (m *Manager) Close() error { return m.cache.Close() }

// Options maps credential data to go-redis options.
func Options(data map[string]any) (*redis.Options, error) {
	host, _ := data["host"].(string)
	if host == "" {
		host = defaultHost
	}
	p, err := intField(data, "port", defaultPort)
	if err != nil {
		return nil, err
	}
	db, err := intField(data, "db", 0)
	if err != nil {
		return nil, err
	}
	if db < 0 {
		return nil, errors.New("db must not be negative")
	}
	o := &redis.Options{Addr: net.JoinHostPort(host, strconv.Itoa(p)), DB: db}
	o.Username, _ = data["username"].(string)
	o.Password, _ = data["password"].(string)
	if useTLS, _ := data["tls"].(bool); useTLS {
		o.TLSConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	return o, nil
}

func intField(data map[string]any, key string, def int) (int, error) {
	switch v := data[key].(type) {
	case nil:
		return def, nil
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case string:
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s %q is not a number", key, v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("%s must be a number", key)
}
