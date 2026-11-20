package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/adapter/bus/memory"
	pgbus "github.com/engi-hq/engi/internal/adapter/bus/postgres"
	"github.com/engi-hq/engi/internal/adapter/clock/system"
	k8slease "github.com/engi-hq/engi/internal/adapter/lease/kubernetes"
	"github.com/engi-hq/engi/internal/adapter/lease/local"
	pglease "github.com/engi-hq/engi/internal/adapter/lease/postgres"
	"github.com/engi-hq/engi/internal/adapter/pgdb"
	pgqueue "github.com/engi-hq/engi/internal/adapter/queue/postgres"
	sqlitequeue "github.com/engi-hq/engi/internal/adapter/queue/sqlite"
	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	pgstore "github.com/engi-hq/engi/internal/adapter/store/postgres"
	sqlitestore "github.com/engi-hq/engi/internal/adapter/store/sqlite"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/migrations"
)

// storeSet is every store port, backed by whichever driver store.driver names.
type storeSet struct {
	Workflows   port.WorkflowStore
	Versions    port.WorkflowVersionStore
	Executions  port.ExecutionStore
	States      port.ExecutionStateStore
	Credentials port.CredentialStore
	Users       port.UserStore
	Sessions    port.SessionStore
	APIKeys     port.APIKeyStore
	Tags        port.TagStore
	Variables   port.VariableStore
	Webhooks    port.WebhookStore
	TestHooks   port.TestWebhookStore
	Triggers    port.TriggerStateStore
	Activations port.ActivationStore
	Tx          port.TxManager
}

// jobQueue is the queue as the runtime uses it.
type jobQueue interface {
	port.JobQueue
	port.JobReclaimer
	port.JobDispatcher
	port.QueueStats
}

// database is an opened store database; target is set for Postgres.
type database struct {
	*sql.DB
	driver string
	target *pgdb.Target
	// clock stamps applied_at when migrations run. It starts as the system
	// clock and storeDatabase replaces it with the App's clock.
	clock port.Clock
}

// openDatabase opens the database named by the store configuration.
// readFile loads the Postgres DSN file.
func openDatabase(ctx context.Context, cfg config.Config, readFile func(string) ([]byte, error)) (*database, error) {
	switch d := cfg.Store.ResolveDriver(); d {
	case config.StorePostgres:
		target, err := pgdb.NewTarget(cfg.Store.Postgres, readFile)
		if err != nil {
			return nil, fmt.Errorf("opening store: %w", err)
		}
		db, err := target.Open(ctx, cfg.Store)
		if err != nil {
			return nil, fmt.Errorf("opening store: %w", err)
		}
		return &database{DB: db, driver: d, target: target, clock: system.Clock{}}, nil
	default:
		db, err := sqlitedb.Open(ctx, cfg.Store)
		if err != nil {
			return nil, fmt.Errorf("opening store: %w", err)
		}
		return &database{DB: db, driver: config.StoreSQLite, clock: system.Clock{}}, nil
	}
}

// storeDatabase returns the one database handle this App shares between the
// credential service and the runtime, opening it on first use. Opening it per
// consumer would give each its own connection pool on the same file.
func (a *App) storeDatabase(ctx context.Context) (*database, error) {
	if a.db != nil {
		return a.db, nil
	}
	db, err := openDatabase(ctx, a.Config, a.readFile)
	if err != nil {
		return nil, err
	}
	db.clock = a.Clock
	a.db = db
	return db, nil
}

// closeDatabase closes the shared handle. It is idempotent: a second call, or a
// call before anything opened the database, does nothing.
func (a *App) closeDatabase() error {
	if a.db == nil {
		return nil
	}
	db := a.db
	a.db = nil
	if err := db.Close(); err != nil {
		return fmt.Errorf("closing store: %w", err)
	}
	return nil
}

// migrate applies pending migrations of the database's driver. applied_at is
// stamped with the handle's clock (the App clock for the shared handle).
func (d *database) migrate(ctx context.Context) error {
	if d.driver == config.StorePostgres {
		return pgdb.Migrate(ctx, d.DB, migrations.Postgres, migrations.PostgresDir, pgdb.WithClock(d.clock))
	}
	return sqlitedb.Migrate(ctx, d.DB, migrations.SQLite, migrations.SQLiteDir, sqlitedb.WithClock(d.clock))
}

func (d *database) migrateDown(ctx context.Context) (int, error) {
	if d.driver == config.StorePostgres {
		return pgdb.MigrateDown(ctx, d.DB, migrations.Postgres, migrations.PostgresDir)
	}
	return sqlitedb.MigrateDown(ctx, d.DB, migrations.SQLite, migrations.SQLiteDir)
}

func (d *database) pending(ctx context.Context) (int, error) {
	if d.driver == config.StorePostgres {
		return pgdb.Pending(ctx, d.DB, migrations.Postgres, migrations.PostgresDir)
	}
	return sqlitedb.Pending(ctx, d.DB, migrations.SQLite, migrations.SQLiteDir)
}

// stores builds the store ports over the database.
func (d *database) stores(cfg config.StoreConfig, clk port.Clock) storeSet {
	if d.driver == config.StorePostgres {
		s := pgstore.New(d.DB, cfg, clk)
		wfs, execs, tr := s.Workflows(), s.Executions(), s.Triggers()
		return storeSet{
			Workflows: wfs, Versions: wfs, Executions: execs, States: execs, Credentials: s.Credentials(),
			Users: s.Users(), Sessions: s.Sessions(), APIKeys: s.APIKeys(), Tags: s.Tags(), Variables: s.Variables(),
			Webhooks: tr, TestHooks: s.TestWebhooks(), Triggers: tr, Activations: tr, Tx: s,
		}
	}
	s := sqlitestore.New(d.DB, cfg, clk)
	wfs, execs, tr := s.Workflows(), s.Executions(), s.Triggers()
	return storeSet{
		Workflows: wfs, Versions: wfs, Executions: execs, States: execs, Credentials: s.Credentials(),
		Users: s.Users(), Sessions: s.Sessions(), APIKeys: s.APIKeys(), Tags: s.Tags(), Variables: s.Variables(),
		Webhooks: tr, TestHooks: s.TestWebhooks(), Triggers: tr, Activations: tr, Tx: s,
	}
}

// chatMemory builds the chat memory store over the database.
func (d *database) chatMemory(cfg config.StoreConfig, mem config.AIMemoryConfig, clk port.Clock) port.ChatMemoryStore {
	if d.driver == config.StorePostgres {
		return pgstore.New(d.DB, cfg, clk).ChatMemory(mem)
	}
	return sqlitestore.New(d.DB, cfg, clk).ChatMemory(mem)
}

// cluster is what the runtime needs besides the stores: queue, bus and leader
// election, each following its driver setting, plus the cleanup that stops
// their background connections.
type cluster struct {
	queue jobQueue
	bus   port.Bus
	lease port.LeaseProvider
	stop  func()
}

// newCluster builds the queue, bus and lease provider for the configuration.
// The Postgres ones open dedicated LISTEN or lock connections; stop closes them.
func (a *App) newCluster(ctx context.Context, db *database) (_ cluster, err error) {
	cfg := a.Config
	var stops []func()
	c := cluster{stop: func() {
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}}
	defer func() {
		if err != nil {
			c.stop()
		}
	}()
	if err := checkDrivers(cfg); err != nil {
		return cluster{}, err
	}
	delay := cfg.Store.Postgres.ListenReconnectDelay

	switch cfg.ResolveQueueDriver() {
	case config.StorePostgres:
		q := pgqueue.New(db.DB, cfg.Queue, a.Clock)
		if err := q.Listen(ctx, db.target, delay, a.Logger.With("component", "queue")); err != nil {
			return cluster{}, err
		}
		stops = append(stops, q.Close)
		c.queue = q
	default:
		c.queue = sqlitequeue.New(db.DB, cfg.Queue, a.Clock)
	}

	switch cfg.ResolveBusDriver() {
	case config.StorePostgres:
		b, err := pgbus.New(db.DB, db.target, cfg.Bus, cfg.Worker.EventBuffer, delay, a.Clock, a.Logger.With("component", "bus"))
		if err != nil {
			return cluster{}, fmt.Errorf("building event bus: %w", err)
		}
		if err := b.Start(ctx); err != nil {
			return cluster{}, err
		}
		stops = append(stops, b.Close)
		c.bus = b
	default:
		b, err := memory.New(cfg.Worker.EventBuffer, a.Logger)
		if err != nil {
			return cluster{}, fmt.Errorf("building event bus: %w", err)
		}
		c.bus = b
	}

	switch cfg.ResolveLeaseType() {
	case config.LeasePostgres:
		c.lease = pglease.New(db.target, cfg.Trigger.Lease, a.Logger.With("component", "lease"))
	case config.LeaseKubernetes:
		l, err := k8slease.New(cfg.Trigger.Lease, k8slease.Options{Logger: a.Logger.With("component", "lease")})
		if err != nil {
			return cluster{}, fmt.Errorf("building kubernetes lease: %w", err)
		}
		c.lease = l
	default:
		c.lease = local.New()
	}
	return c, nil
}

// checkDrivers rejects combinations that cannot work: the Postgres queue, bus
// and lease need the Postgres store, and the SQLite queue needs the SQLite one.
func checkDrivers(cfg config.Config) error {
	store := cfg.Store.ResolveDriver()
	var errs []error
	if q := cfg.ResolveQueueDriver(); q != store {
		errs = append(errs, fmt.Errorf("queue.driver %q does not match store.driver %q: the queue lives in the store's database", q, store))
	}
	if cfg.ResolveBusDriver() == config.StorePostgres && store != config.StorePostgres {
		errs = append(errs, errors.New("bus.driver postgres needs store.driver postgres"))
	}
	if cfg.ResolveLeaseType() == config.LeasePostgres && store != config.StorePostgres {
		errs = append(errs, errors.New("lease.type postgres needs store.driver postgres"))
	}
	return errors.Join(errs...)
}
