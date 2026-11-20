// Package pgdb opens the Postgres database shared by the cluster-mode store,
// queue, bus and lease adapters, applies migrations, and provides transaction
// plumbing and LISTEN/NOTIFY connections.
//
// Statements go through database/sql (pgx stdlib driver) so the adapters can
// mirror their SQLite counterparts. Queries are written with "?" placeholders
// and rewritten to "$n" by Rebind.
package pgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/engi-hq/engi/internal/config"
)

// Stamp converts t to the value stored in a timestamptz column: UTC, at the
// microsecond precision Postgres keeps, so values round-trip unchanged.
func Stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// FromDB normalizes a scanned timestamptz to UTC.
func FromDB(t time.Time) time.Time { return t.UTC() }

// Target is a parsed connection target. It opens the pooled *sql.DB and the
// dedicated connections used for LISTEN and advisory locks.
type Target struct {
	conn *pgx.ConnConfig
}

// NewTarget resolves the DSN from cfg (the file wins over the inline value) and
// parses it. readFile loads DSNFile.
func NewTarget(cfg config.PostgresConfig, readFile func(string) ([]byte, error)) (*Target, error) {
	dsn := cfg.DSN
	if cfg.DSNFile != "" {
		b, err := readFile(cfg.DSNFile)
		if err != nil {
			return nil, fmt.Errorf("reading postgres dsn file: %w", err)
		}
		dsn = strings.TrimSpace(string(b))
	}
	if dsn == "" {
		return nil, errors.New("postgres dsn is empty")
	}
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		// pgx errors can echo the DSN; do not wrap the cause.
		return nil, errors.New("parsing postgres dsn: invalid connection string")
	}
	cc.ConnectTimeout = cfg.ConnectTimeout
	if cfg.ApplicationName != "" {
		cc.RuntimeParams["application_name"] = cfg.ApplicationName
	}
	return &Target{conn: cc}, nil
}

// Open opens the connection pool sized by cfg and verifies connectivity.
func (t *Target) Open(ctx context.Context, cfg config.StoreConfig) (*sql.DB, error) {
	db := stdlib.OpenDB(*t.conn.Copy())
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.Postgres.ConnMaxIdleTime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	return db, nil
}

// Connect opens one dedicated connection, outside the pool.
func (t *Target) Connect(ctx context.Context) (*pgx.Conn, error) {
	c, err := pgx.ConnectConfig(ctx, t.conn.Copy())
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	return c, nil
}

// Rebind rewrites "?" placeholders to "$1", "$2", ... outside string literals.
func Rebind(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	n, inQuote := 0, false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			fmt.Fprintf(&b, "$%d", n)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
