package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Supported SQLite journal modes and synchronous levels.
const (
	JournalWAL    = "wal"
	JournalDelete = "delete"

	SyncOff    = "off"
	SyncNormal = "normal"
	SyncFull   = "full"
)

// Persistence drivers (store.driver). Queue, bus and lease drivers additionally
// accept DriverAuto, which follows the store driver.
const (
	StoreSQLite   = "sqlite"
	StorePostgres = "postgres"

	DriverAuto = "auto"
	// BusMemory is the in-process bus.
	BusMemory = "memory"
)

// StoreConfig tunes the store. The SQLite settings apply to driver sqlite, the
// Postgres section to driver postgres; pool sizes and page sizes apply to both.
// The queue shares the store's database.
type StoreConfig struct {
	// Driver is sqlite (default, single process) or postgres (cluster mode).
	Driver string `yaml:"driver"`
	// Postgres configures the postgres driver.
	Postgres PostgresConfig `yaml:"postgres"`
	// Path is the database file. In-memory databases are not supported
	// because every pooled connection would see a different database.
	Path string `yaml:"path"`
	// JournalMode is wal or delete.
	JournalMode string `yaml:"journalMode"`
	// Synchronous is off, normal or full.
	Synchronous string `yaml:"synchronous"`
	// BusyTimeout is how long a connection waits for a lock held by another.
	BusyTimeout time.Duration `yaml:"busyTimeout"`
	// MaxOpenConns and MaxIdleConns size the connection pool.
	MaxOpenConns int `yaml:"maxOpenConns"`
	MaxIdleConns int `yaml:"maxIdleConns"`
	// ConnMaxLifetime recycles pooled connections; zero keeps them forever.
	ConnMaxLifetime time.Duration `yaml:"connMaxLifetime"`
	// DefaultPageSize applies to listings that do not ask for a size;
	// MaxPageSize caps any requested size.
	DefaultPageSize int `yaml:"defaultPageSize"`
	MaxPageSize     int `yaml:"maxPageSize"`
}

// PostgresConfig configures the Postgres store, queue, bus and lease adapters,
// which share one connection pool plus dedicated listener connections.
type PostgresConfig struct {
	// DSN is the connection string (postgres://user:pass@host/db). It carries
	// the password, so prefer DSNFile or the ENGI_STORE_POSTGRES_DSN variable
	// over a config file. It is never logged.
	DSN string `yaml:"dsn"`
	// DSNFile is a file holding the DSN (a mounted secret). It wins over DSN.
	DSNFile string `yaml:"dsnFile"`
	// ConnectTimeout bounds opening a new connection.
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// ConnMaxIdleTime closes pooled connections idle for this long; zero keeps them.
	ConnMaxIdleTime time.Duration `yaml:"connMaxIdleTime"`
	// ApplicationName shows in pg_stat_activity.
	ApplicationName string `yaml:"applicationName"`
	// ListenReconnectDelay is the pause before a lost LISTEN connection (queue
	// wake-ups, event bus) is re-established.
	ListenReconnectDelay time.Duration `yaml:"listenReconnectDelay"`
}

// BusConfig selects and tunes the event bus.
type BusConfig struct {
	// Driver is auto (memory for sqlite, postgres for postgres), memory or postgres.
	Driver string `yaml:"driver"`
	// MaxInlineBytes is the largest event payload sent inside a NOTIFY. Larger
	// events are stored in the bus_events table and only their id is notified.
	// Postgres limits a NOTIFY payload to just under 8000 bytes.
	MaxInlineBytes int `yaml:"maxInlineBytes"`
	// EventRetention is how long stored large events are kept before pruning.
	EventRetention time.Duration `yaml:"eventRetention"`
}

// QueueConfig tunes the durable job queue.
type QueueConfig struct {
	// Driver is auto (follows store.driver), sqlite or postgres. It must match
	// store.driver because the queue lives in the store's database.
	Driver string `yaml:"driver"`
	// LeaseDuration is how long a claimed job stays owned without a heartbeat.
	LeaseDuration time.Duration `yaml:"leaseDuration"`
	// HeartbeatInterval is how often a worker extends its lease. It must be
	// shorter than LeaseDuration.
	HeartbeatInterval time.Duration `yaml:"heartbeatInterval"`
	// PollInterval is the fallback wait between claim attempts when no
	// in-process wakeup arrives (for example, jobs enqueued by another process).
	PollInterval time.Duration `yaml:"pollInterval"`
	// MaxAttempts is how many times a job may be claimed before it is parked
	// as failed.
	MaxAttempts int `yaml:"maxAttempts"`
	// RetryDelay is how long a failed job waits before it can be claimed again.
	RetryDelay time.Duration `yaml:"retryDelay"`
}

func defaultStore() StoreConfig {
	return StoreConfig{
		Driver: StoreSQLite,
		Postgres: PostgresConfig{
			ConnectTimeout: 10 * time.Second, ConnMaxIdleTime: 5 * time.Minute,
			ApplicationName: "engi", ListenReconnectDelay: 2 * time.Second,
		},
		Path:            "engi.db",
		JournalMode:     JournalWAL,
		Synchronous:     SyncNormal,
		BusyTimeout:     5 * time.Second,
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: 0,
		DefaultPageSize: 50,
		MaxPageSize:     500,
	}
}

func defaultQueue() QueueConfig {
	return QueueConfig{
		Driver:            DriverAuto,
		LeaseDuration:     2 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		PollInterval:      time.Second,
		MaxAttempts:       3,
		RetryDelay:        5 * time.Second,
	}
}

func defaultBus() BusConfig {
	return BusConfig{Driver: DriverAuto, MaxInlineBytes: 7000, EventRetention: 5 * time.Minute}
}

// Validate reports every problem with the bus settings.
func (c BusConfig) Validate() error {
	var errs []error
	if !slices.Contains([]string{DriverAuto, BusMemory, StorePostgres}, c.Driver) {
		errs = append(errs, fmt.Errorf("bus.driver %q is not one of auto, memory, postgres", c.Driver))
	}
	errs = appendPositive(errs, "bus.maxInlineBytes", int64(c.MaxInlineBytes))
	errs = appendPositive(errs, "bus.eventRetention", int64(c.EventRetention))
	return errors.Join(errs...)
}

// ResolveDriver returns the store's driver (sqlite when unset).
func (c StoreConfig) ResolveDriver() string {
	if c.Driver == "" {
		return StoreSQLite
	}
	return c.Driver
}

// ResolveQueueDriver turns auto into the store driver.
func (c Config) ResolveQueueDriver() string {
	if c.Queue.Driver == DriverAuto || c.Queue.Driver == "" {
		return c.Store.ResolveDriver()
	}
	return c.Queue.Driver
}

// ResolveBusDriver turns auto into postgres for a postgres store, else memory.
func (c Config) ResolveBusDriver() string {
	if c.Bus.Driver == DriverAuto || c.Bus.Driver == "" {
		if c.Store.ResolveDriver() == StorePostgres {
			return StorePostgres
		}
		return BusMemory
	}
	return c.Bus.Driver
}

// ResolveLeaseType turns auto into postgres for a postgres store, else local.
func (c Config) ResolveLeaseType() string {
	if c.Trigger.Lease.Type == DriverAuto || c.Trigger.Lease.Type == "" {
		if c.Store.ResolveDriver() == StorePostgres {
			return LeasePostgres
		}
		return LeaseLocal
	}
	return c.Trigger.Lease.Type
}

// Validate reports every problem with the store settings.
func (c StoreConfig) Validate() error {
	var errs []error
	switch c.Driver {
	case StoreSQLite, "":
		if c.Path == "" {
			errs = append(errs, errors.New("store.path must not be empty"))
		}
		if !slices.Contains([]string{JournalWAL, JournalDelete}, c.JournalMode) {
			errs = append(errs, fmt.Errorf("store.journalMode %q is not one of wal, delete", c.JournalMode))
		}
		if !slices.Contains([]string{SyncOff, SyncNormal, SyncFull}, c.Synchronous) {
			errs = append(errs, fmt.Errorf("store.synchronous %q is not one of off, normal, full", c.Synchronous))
		}
		errs = appendPositive(errs, "store.busyTimeout", int64(c.BusyTimeout))
	case StorePostgres:
		errs = append(errs, c.Postgres.validate()...)
	default:
		errs = append(errs, fmt.Errorf("store.driver %q is not one of sqlite, postgres", c.Driver))
	}
	errs = appendPositive(errs, "store.maxOpenConns", int64(c.MaxOpenConns))
	errs = appendPositive(errs, "store.defaultPageSize", int64(c.DefaultPageSize))
	if c.MaxPageSize < c.DefaultPageSize {
		errs = append(errs, errors.New("store.maxPageSize must not be smaller than store.defaultPageSize"))
	}
	if c.MaxIdleConns < 0 {
		errs = append(errs, errors.New("store.maxIdleConns must not be negative"))
	}
	if c.ConnMaxLifetime < 0 {
		errs = append(errs, errors.New("store.connMaxLifetime must not be negative"))
	}
	return errors.Join(errs...)
}

func (c PostgresConfig) validate() []error {
	var errs []error
	if c.DSN == "" && c.DSNFile == "" {
		errs = append(errs, errors.New("store.postgres.dsn or store.postgres.dsnFile is required with store.driver postgres "+
			"(set ENGI_STORE_POSTGRES_DSN or ENGI_STORE_POSTGRES_DSN_FILE)"))
	}
	errs = appendPositive(errs, "store.postgres.connectTimeout", int64(c.ConnectTimeout))
	errs = appendPositive(errs, "store.postgres.listenReconnectDelay", int64(c.ListenReconnectDelay))
	if c.ConnMaxIdleTime < 0 {
		errs = append(errs, errors.New("store.postgres.connMaxIdleTime must not be negative"))
	}
	return errs
}

// Validate reports every problem with the queue settings.
func (c QueueConfig) Validate() error {
	var errs []error
	if !slices.Contains([]string{DriverAuto, StoreSQLite, StorePostgres}, c.Driver) {
		errs = append(errs, fmt.Errorf("queue.driver %q is not one of auto, sqlite, postgres", c.Driver))
	}
	errs = appendPositive(errs, "queue.leaseDuration", int64(c.LeaseDuration))
	errs = appendPositive(errs, "queue.heartbeatInterval", int64(c.HeartbeatInterval))
	errs = appendPositive(errs, "queue.pollInterval", int64(c.PollInterval))
	errs = appendPositive(errs, "queue.maxAttempts", int64(c.MaxAttempts))
	if c.RetryDelay < 0 {
		errs = append(errs, errors.New("queue.retryDelay must not be negative"))
	}
	if c.HeartbeatInterval >= c.LeaseDuration && c.LeaseDuration > 0 {
		errs = append(errs, errors.New("queue.heartbeatInterval must be shorter than queue.leaseDuration"))
	}
	return errors.Join(errs...)
}

// storeSettings are the env/flag bindings of the store and queue sections.
func storeSettings() []setting {
	dur := func(dst func(*Config) *time.Duration) func(*Config, string) error {
		return func(c *Config, v string) (err error) { *dst(c), err = time.ParseDuration(v); return }
	}
	num := func(dst func(*Config) *int) func(*Config, string) error {
		return func(c *Config, v string) (err error) { *dst(c), err = strconv.Atoi(v); return }
	}
	str := func(dst func(*Config) *string) func(*Config, string) error {
		return func(c *Config, v string) error { *dst(c) = v; return nil }
	}
	return []setting{
		{"ENGI_STORE_DRIVER", "store-driver", "store driver: sqlite, postgres",
			str(func(c *Config) *string { return &c.Store.Driver })},
		{"ENGI_STORE_POSTGRES_DSN", "store-postgres-dsn", "Postgres DSN (prefer the env var or the DSN file)",
			str(func(c *Config) *string { return &c.Store.Postgres.DSN })},
		{"ENGI_STORE_POSTGRES_DSN_FILE", "store-postgres-dsn-file", "file holding the Postgres DSN",
			str(func(c *Config) *string { return &c.Store.Postgres.DSNFile })},
		{"ENGI_STORE_POSTGRES_CONNECT_TIMEOUT", "store-postgres-connect-timeout", "Postgres connect timeout",
			dur(func(c *Config) *time.Duration { return &c.Store.Postgres.ConnectTimeout })},
		{"ENGI_STORE_POSTGRES_CONN_MAX_IDLE_TIME", "store-postgres-conn-max-idle-time", "Postgres pooled connection max idle time",
			dur(func(c *Config) *time.Duration { return &c.Store.Postgres.ConnMaxIdleTime })},
		{"ENGI_STORE_POSTGRES_APPLICATION_NAME", "store-postgres-application-name", "Postgres application_name",
			str(func(c *Config) *string { return &c.Store.Postgres.ApplicationName })},
		{"ENGI_STORE_POSTGRES_LISTEN_RECONNECT_DELAY", "store-postgres-listen-reconnect-delay", "pause before re-establishing a LISTEN connection",
			dur(func(c *Config) *time.Duration { return &c.Store.Postgres.ListenReconnectDelay })},
		{"ENGI_QUEUE_DRIVER", "queue-driver", "queue driver: auto, sqlite, postgres",
			str(func(c *Config) *string { return &c.Queue.Driver })},
		{"ENGI_BUS_DRIVER", "bus-driver", "event bus driver: auto, memory, postgres",
			str(func(c *Config) *string { return &c.Bus.Driver })},
		{"ENGI_BUS_MAX_INLINE_BYTES", "bus-max-inline-bytes", "largest event sent inline in a NOTIFY",
			num(func(c *Config) *int { return &c.Bus.MaxInlineBytes })},
		{"ENGI_BUS_EVENT_RETENTION", "bus-event-retention", "how long stored large bus events are kept",
			dur(func(c *Config) *time.Duration { return &c.Bus.EventRetention })},
		{"ENGI_STORE_PATH", "store-path", "SQLite database file",
			func(c *Config, v string) error { c.Store.Path = v; return nil }},
		{"ENGI_STORE_JOURNAL_MODE", "store-journal-mode", "SQLite journal mode: wal, delete",
			func(c *Config, v string) error { c.Store.JournalMode = v; return nil }},
		{"ENGI_STORE_SYNCHRONOUS", "store-synchronous", "SQLite synchronous level: off, normal, full",
			func(c *Config, v string) error { c.Store.Synchronous = v; return nil }},
		{"ENGI_STORE_BUSY_TIMEOUT", "store-busy-timeout", "SQLite lock wait timeout",
			dur(func(c *Config) *time.Duration { return &c.Store.BusyTimeout })},
		{"ENGI_STORE_MAX_OPEN_CONNS", "store-max-open-conns", "database pool max open connections",
			num(func(c *Config) *int { return &c.Store.MaxOpenConns })},
		{"ENGI_STORE_MAX_IDLE_CONNS", "store-max-idle-conns", "database pool max idle connections",
			num(func(c *Config) *int { return &c.Store.MaxIdleConns })},
		{"ENGI_STORE_CONN_MAX_LIFETIME", "store-conn-max-lifetime", "database connection max lifetime",
			dur(func(c *Config) *time.Duration { return &c.Store.ConnMaxLifetime })},
		{"ENGI_STORE_DEFAULT_PAGE_SIZE", "store-default-page-size", "default list page size",
			num(func(c *Config) *int { return &c.Store.DefaultPageSize })},
		{"ENGI_STORE_MAX_PAGE_SIZE", "store-max-page-size", "maximum list page size",
			num(func(c *Config) *int { return &c.Store.MaxPageSize })},
		{"ENGI_QUEUE_LEASE_DURATION", "queue-lease-duration", "job lease duration",
			dur(func(c *Config) *time.Duration { return &c.Queue.LeaseDuration })},
		{"ENGI_QUEUE_HEARTBEAT_INTERVAL", "queue-heartbeat-interval", "job lease heartbeat interval",
			dur(func(c *Config) *time.Duration { return &c.Queue.HeartbeatInterval })},
		{"ENGI_QUEUE_POLL_INTERVAL", "queue-poll-interval", "fallback job poll interval",
			dur(func(c *Config) *time.Duration { return &c.Queue.PollInterval })},
		{"ENGI_QUEUE_MAX_ATTEMPTS", "queue-max-attempts", "max claims of one job",
			num(func(c *Config) *int { return &c.Queue.MaxAttempts })},
		{"ENGI_QUEUE_RETRY_DELAY", "queue-retry-delay", "delay before a failed job is retried",
			dur(func(c *Config) *time.Duration { return &c.Queue.RetryDelay })},
	}
}
