package port

import (
	"context"
	"database/sql"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Supported database drivers, the driver argument of DBPool.DB.
const (
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
)

// DBPool hands out shared connection pools. There is one pool per credential
// id and credential version: editing a credential makes the next call build a
// new pool and close the old one. Callers must not close the returned *sql.DB.
type DBPool interface {
	DB(ctx context.Context, driver string, cred nodesdk.Credential) (*sql.DB, error)
}
