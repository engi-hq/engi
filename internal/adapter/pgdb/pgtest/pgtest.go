//go:build integration

// Package pgtest starts a throwaway Postgres in Docker (testcontainers) for the
// integration tests of the Postgres adapters. One container serves a whole test
// process; every call to New gets its own freshly created database, so tests
// stay independent without paying for a container each.
package pgtest

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/engi-hq/engi/internal/adapter/pgdb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/migrations"
)

const (
	image       = "postgres:16-alpine"
	adminDB     = "postgres"
	user        = "engi"
	password    = "secret"
	startupWait = 3 * time.Minute
)

var (
	once      sync.Once
	baseURL   *url.URL
	startErr  error
	dbCounter atomic.Int64
)

func start() {
	ctx, cancel := context.WithTimeout(context.Background(), startupWait)
	defer cancel()
	c, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(adminDB), tcpostgres.WithUsername(user), tcpostgres.WithPassword(password),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		startErr = fmt.Errorf("starting postgres container: %w", err)
		return
	}
	host, err := c.Host(ctx)
	if err != nil {
		startErr = err
		return
	}
	p, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		startErr = err
		return
	}
	baseURL = &url.URL{
		Scheme: "postgres", User: url.UserPassword(user, password),
		Host: net.JoinHostPort(host, p.Port()), Path: "/" + adminDB, RawQuery: "sslmode=disable",
	}
	// The container is removed by testcontainers' reaper when the process ends.
	_ = testcontainers.Container(c)
}

// Env is one isolated, migrated database.
type Env struct {
	// DSN connects to the database.
	DSN string
	// Target opens connections to it.
	Target *pgdb.Target
	// DB is a pool on it, closed at test cleanup.
	DB *sql.DB
	// Store is a store configuration pointing at it (driver postgres).
	Store config.StoreConfig
}

// New creates a fresh database in the shared container and migrates it.
func New(t *testing.T) *Env {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("docker postgres unavailable: %v", startErr)
		}
		t.Fatal(startErr)
	}
	ctx := context.Background()
	name := fmt.Sprintf("t%d_%d", time.Now().UnixNano(), dbCounter.Add(1))
	admin, err := pgx.Connect(ctx, baseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u := *baseURL
	u.Path = "/" + name
	dsn := u.String()

	cfg := config.Default().Store
	cfg.Driver = config.StorePostgres
	cfg.Postgres.DSN = dsn
	target, err := pgdb.NewTarget(cfg.Postgres, func(string) ([]byte, error) { return nil, os.ErrNotExist })
	if err != nil {
		t.Fatal(err)
	}
	db, err := target.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := pgdb.Migrate(ctx, db, migrations.Postgres, migrations.PostgresDir); err != nil {
		t.Fatal(err)
	}
	return &Env{DSN: dsn, Target: target, DB: db, Store: cfg}
}

// Terminate kills every other backend connected to the database whose
// application name contains match (all of them when empty), to simulate a
// network drop.
func (e *Env) Terminate(t *testing.T, match string) int {
	t.Helper()
	rows, err := e.DB.Query(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND application_name LIKE $1`, "%"+strings.TrimSpace(match)+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n
}
