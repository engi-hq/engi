// Package porttest holds reusable contract tests for the persistence and queue
// ports. An adapter's test file passes a constructor to RunStoreSuite or
// RunQueueSuite; the SQLite adapters and the future Postgres adapters run the
// exact same behaviour checks.
package porttest
