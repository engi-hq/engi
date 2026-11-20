// Package postgres implements the Postgres node (engi.db.postgres): execute
// query, select, insert, update, upsert and delete through a shared pool.
package postgres

import (
	"github.com/engi-hq/engi/internal/credtype/postgres"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.db.postgres"

// New returns a Postgres node that gets its connections from pools.
func New(pools sqlnode.Pools) nodesdk.Node {
	return sqlnode.New(sqlnode.Spec{
		Dialect: sqlnode.Postgres, Type: Type, DisplayName: "Postgres", CredentialType: postgres.Name,
		Schema: true, Placeholder: "$1, $2, ...",
	}, pools)
}
