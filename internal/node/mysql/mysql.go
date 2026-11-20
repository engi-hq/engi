// Package mysql implements the MySQL node (engi.db.mysql): execute query,
// select, insert, update, upsert and delete through a shared pool.
package mysql

import (
	"github.com/engi-hq/engi/internal/credtype/mysql"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.db.mysql"

// New returns a MySQL node that gets its connections from pools.
func New(pools sqlnode.Pools) nodesdk.Node {
	return sqlnode.New(sqlnode.Spec{
		Dialect: sqlnode.MySQL, Type: Type, DisplayName: "MySQL", CredentialType: mysql.Name, Placeholder: "?",
	}, pools)
}
