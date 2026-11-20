package config

import (
	"errors"
	"strconv"
	"time"
)

// WorkerConfig tunes the execution worker, the wait scheduler and synchronous
// runs. Lease and heartbeat timings belong to QueueConfig.
type WorkerConfig struct {
	// Concurrency is the number of executions one worker runs at once.
	Concurrency int `yaml:"concurrency"`
	// ReclaimInterval is how often jobs with an expired lease are recovered.
	ReclaimInterval time.Duration `yaml:"reclaimInterval"`
	// ShutdownGrace is how long a stopping worker lets running executions
	// finish before it cancels them and releases their jobs.
	ShutdownGrace time.Duration `yaml:"shutdownGrace"`
	// PersistTimeout bounds the final writes of an execution (its result, its
	// job), which must still succeed after the execution's context ended.
	PersistTimeout time.Duration `yaml:"persistTimeout"`
	// WaiterInterval is how often due waiting executions are looked up.
	WaiterInterval time.Duration `yaml:"waiterInterval"`
	// WaiterBatchSize caps the executions resumed per waiter tick.
	WaiterBatchSize int `yaml:"waiterBatchSize"`
	// SyncRunTimeout bounds how long a synchronous run waits for the result.
	SyncRunTimeout time.Duration `yaml:"syncRunTimeout"`
	// EventBuffer is how many events an in-process bus subscriber may lag
	// behind before events are dropped for it.
	EventBuffer int `yaml:"eventBuffer"`
	// SyncPollInterval is the fallback store check of a synchronous run, for
	// results announced by another process.
	SyncPollInterval time.Duration `yaml:"syncPollInterval"`
}

func defaultWorker() WorkerConfig {
	return WorkerConfig{
		Concurrency:      4,
		ReclaimInterval:  30 * time.Second,
		ShutdownGrace:    30 * time.Second,
		PersistTimeout:   30 * time.Second,
		WaiterInterval:   time.Second,
		WaiterBatchSize:  100,
		SyncRunTimeout:   5 * time.Minute,
		SyncPollInterval: time.Second,
		EventBuffer:      256,
	}
}

// Validate reports every problem with the worker settings.
func (c WorkerConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "worker.concurrency", int64(c.Concurrency))
	errs = appendPositive(errs, "worker.reclaimInterval", int64(c.ReclaimInterval))
	errs = appendPositive(errs, "worker.persistTimeout", int64(c.PersistTimeout))
	errs = appendPositive(errs, "worker.waiterInterval", int64(c.WaiterInterval))
	errs = appendPositive(errs, "worker.waiterBatchSize", int64(c.WaiterBatchSize))
	errs = appendPositive(errs, "worker.syncRunTimeout", int64(c.SyncRunTimeout))
	errs = appendPositive(errs, "worker.syncPollInterval", int64(c.SyncPollInterval))
	errs = appendPositive(errs, "worker.eventBuffer", int64(c.EventBuffer))
	if c.ShutdownGrace < 0 {
		errs = append(errs, errors.New("worker.shutdownGrace must not be negative"))
	}
	return errors.Join(errs...)
}

// workerSettings are the env/flag bindings of the worker section.
func workerSettings() []setting {
	dur := func(dst func(*Config) *time.Duration) func(*Config, string) error {
		return func(c *Config, v string) (err error) { *dst(c), err = time.ParseDuration(v); return }
	}
	num := func(dst func(*Config) *int) func(*Config, string) error {
		return func(c *Config, v string) (err error) { *dst(c), err = strconv.Atoi(v); return }
	}
	return []setting{
		{"ENGI_WORKER_CONCURRENCY", "worker-concurrency", "executions run in parallel by one worker",
			num(func(c *Config) *int { return &c.Worker.Concurrency })},
		{"ENGI_WORKER_RECLAIM_INTERVAL", "worker-reclaim-interval", "how often expired job leases are recovered",
			dur(func(c *Config) *time.Duration { return &c.Worker.ReclaimInterval })},
		{"ENGI_WORKER_SHUTDOWN_GRACE", "worker-shutdown-grace", "time running executions get to finish on shutdown",
			dur(func(c *Config) *time.Duration { return &c.Worker.ShutdownGrace })},
		{"ENGI_WORKER_PERSIST_TIMEOUT", "worker-persist-timeout", "time allowed for the final writes of an execution",
			dur(func(c *Config) *time.Duration { return &c.Worker.PersistTimeout })},
		{"ENGI_WORKER_WAITER_INTERVAL", "worker-waiter-interval", "how often due waiting executions are resumed",
			dur(func(c *Config) *time.Duration { return &c.Worker.WaiterInterval })},
		{"ENGI_WORKER_WAITER_BATCH_SIZE", "worker-waiter-batch-size", "waiting executions resumed per tick",
			num(func(c *Config) *int { return &c.Worker.WaiterBatchSize })},
		{"ENGI_WORKER_SYNC_RUN_TIMEOUT", "worker-sync-run-timeout", "how long a synchronous run waits for its result",
			dur(func(c *Config) *time.Duration { return &c.Worker.SyncRunTimeout })},
		{"ENGI_WORKER_EVENT_BUFFER", "worker-event-buffer", "events an in-process subscriber may lag behind",
			num(func(c *Config) *int { return &c.Worker.EventBuffer })},
		{"ENGI_WORKER_SYNC_POLL_INTERVAL", "worker-sync-poll-interval", "fallback store check of a synchronous run",
			dur(func(c *Config) *time.Duration { return &c.Worker.SyncPollInterval })},
	}
}
