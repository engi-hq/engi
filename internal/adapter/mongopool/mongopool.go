// Package mongopool hands out one MongoDB client per credential id and version.
package mongopool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/engi-hq/engi/internal/clientcache"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const defaultPort = 27017

// Config sizes every client. See config.DatabaseNodeConfig.
type Config struct {
	MaxPoolSize    int
	IdleTimeout    time.Duration
	ConnectTimeout time.Duration
}

// Handle is a connected client plus the database the credential names.
type Handle struct {
	Client   *mongo.Client
	Database string
}

// Manager is safe for concurrent use.
type Manager struct {
	cfg   Config
	cache *clientcache.Cache[Handle]
}

// New returns a manager with no clients open.
func New(cfg Config, log *slog.Logger) *Manager {
	m := &Manager{cfg: cfg}
	m.cache = clientcache.New(log, m.disconnect)
	return m
}

// disconnect closes one client. Closing has no caller context (it runs at
// shutdown or when a credential changes), so it is bounded by the connect timeout.
func (m *Manager) disconnect(h Handle) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ConnectTimeout)
	defer cancel()
	if err := h.Client.Disconnect(ctx); err != nil {
		return fmt.Errorf("disconnecting mongodb client: %w", err)
	}
	return nil
}

// Mongo returns the shared client for the credential and the database it names.
// The driver connects in the background, so a wrong host surfaces as a command error.
func (m *Manager) Mongo(ctx context.Context, cred nodesdk.Credential) (*mongo.Client, string, error) {
	h, err := m.get(ctx, cred)
	return h.Client, h.Database, err
}

func (m *Manager) get(ctx context.Context, cred nodesdk.Credential) (Handle, error) {
	return m.cache.Get(ctx, "mongodb", cred, func() (Handle, error) {
		uri, dbName, useTLS, err := ConnectionString(cred.Data)
		if err != nil {
			return Handle{}, fmt.Errorf("building mongodb connection: %w", err)
		}
		maxPool := uint64(m.cfg.MaxPoolSize) //nolint:gosec // validated positive by config
		opts := options.Client().ApplyURI(uri).SetMaxPoolSize(maxPool).
			SetMaxConnIdleTime(m.cfg.IdleTimeout).SetConnectTimeout(m.cfg.ConnectTimeout).
			SetServerSelectionTimeout(m.cfg.ConnectTimeout)
		if useTLS {
			opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
		}
		client, err := mongo.Connect(opts)
		if err != nil {
			// driver errors can echo the connection string; do not forward them.
			return Handle{}, errors.New("invalid mongodb connection settings")
		}
		return Handle{Client: client, Database: dbName}, nil
	})
}

// Len reports the number of live clients.
func (m *Manager) Len() int { return m.cache.Len() }

// Close closes every client and rejects further requests.
func (m *Manager) Close() error { return m.cache.Close() }

// ConnectionString returns the URI, default database and TLS flag for the
// credential: its connectionString when set, otherwise one built from the
// host/port/user/password fields.
func ConnectionString(data map[string]any) (uri, database string, useTLS bool, err error) {
	database, _ = data["database"].(string)
	useTLS, _ = data["tls"].(bool)
	if cs, _ := data["connectionString"].(string); cs != "" {
		return cs, database, useTLS, nil
	}
	host, _ := data["host"].(string)
	if host == "" {
		return "", "", false, errors.New("connectionString or host is required")
	}
	p := defaultPort
	switch v := data["port"].(type) {
	case nil:
	case float64:
		p = int(v)
	case int:
		p = v
	case int64:
		p = int(v)
	case string:
		if v != "" {
			if p, err = strconv.Atoi(v); err != nil {
				return "", "", false, fmt.Errorf("port %q is not a number", v)
			}
		}
	default:
		return "", "", false, errors.New("port must be a number")
	}
	u := url.URL{Scheme: "mongodb", Host: net.JoinHostPort(host, strconv.Itoa(p))}
	if user, _ := data["user"].(string); user != "" {
		pw, _ := data["password"].(string)
		u.User = url.UserPassword(user, pw)
	}
	return u.String(), database, useTLS, nil
}
