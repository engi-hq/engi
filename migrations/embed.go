// Package migrations embeds the versioned SQL migrations of every driver.
package migrations

import "embed"

// SQLite holds the SQLite migrations under the "sqlite" directory.
//
//go:embed sqlite/*.sql
var SQLite embed.FS

// SQLiteDir is the directory of SQLite migrations inside SQLite.
const SQLiteDir = "sqlite"

// Postgres holds the Postgres migrations under the "postgres" directory. They
// mirror the SQLite ones one to one; further Postgres-only migrations follow.
//
//go:embed postgres/*.sql
var Postgres embed.FS

// PostgresDir is the directory of Postgres migrations inside Postgres.
const PostgresDir = "postgres"
