// Package mongodb implements the MongoDB node (engi.db.mongodb): find,
// find one and update, insert, update, delete and aggregate.
package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"

	credmongo "github.com/engi-hq/engi/internal/credtype/mongodb"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.db.mongodb"

// Clients hands out shared MongoDB clients (satisfied by mongopool.Manager).
// It returns the client and the database the credential names.
type Clients interface {
	Mongo(ctx context.Context, cred nodesdk.Credential) (*mongo.Client, string, error)
}

// Operations.
const (
	opFind          = "find"
	opFindOneUpdate = "findOneAndUpdate"
	opInsert        = "insert"
	opUpdate        = "update"
	opDelete        = "delete"
	opAggregate     = "aggregate"
)

// Values of returnDocument.
const (
	returnAfter  = "after"
	returnBefore = "before"
)

// Node is the MongoDB node.
type Node struct {
	clients    Clients
	maxResults int
}

// New returns a MongoDB node. maxResults caps the documents one find or
// aggregate returns (nodes.database.mongoMaxResults). Without clients Execute
// fails with nodesdk.ErrUnavailable.
func New(clients Clients, maxResults int) *Node {
	return &Node{clients: clients, maxResults: maxResults}
}

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := [][2]string{
		{opFind, "Find"}, {opFindOneUpdate, "Find one and update"}, {opInsert, "Insert"},
		{opUpdate, "Update"}, {opDelete, "Delete"}, {opAggregate, "Aggregate"},
	}
	opts := make([]nodesdk.Option, len(ops))
	for i, o := range ops {
		opts[i] = nodekit.Opt(o[0], o[1])
	}
	filtered := nodekit.Show("operation", opFind, opFindOneUpdate, opUpdate, opDelete)
	updating := nodekit.Show("operation", opFindOneUpdate, opUpdate)
	both := nodekit.Show("operation", opFind, opFindOneUpdate)
	const jsonHelp = " Extended JSON is accepted, e.g. {\"_id\": {\"$oid\": \"...\"}}."
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "MongoDB", Group: nodesdk.GroupAction, Icon: "database",
		Description: "Query and modify MongoDB documents: find, find one and update, insert, update, delete or aggregate.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credmongo.Name, Type: credmongo.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opFind, Options: opts},
			{Name: "collection", DisplayName: "Collection", Type: nodesdk.PropString, Required: true},
			{Name: "filter", DisplayName: "Filter", Type: nodesdk.PropJSON, DisplayOptions: filtered,
				Description: "Query document. Update and Delete refuse an empty filter." + jsonHelp},
			{Name: "projection", DisplayName: "Projection", Type: nodesdk.PropJSON, DisplayOptions: both,
				Description: "Fields to include or exclude, e.g. {\"name\": 1}."},
			{Name: "sort", DisplayName: "Sort", Type: nodesdk.PropJSON, DisplayOptions: both,
				Description: "e.g. {\"createdAt\": -1}."},
			{Name: "limit", DisplayName: "Limit", Type: nodesdk.PropNumber, Default: float64(0), DisplayOptions: nodekit.Show("operation", opFind),
				Description: "Maximum documents to return. 0 returns up to nodes.database.mongoMaxResults."},
			{Name: "update", DisplayName: "Update", Type: nodesdk.PropJSON, DisplayOptions: updating,
				Description: "Update operators such as {\"$set\": {\"status\": \"done\"}}, or an aggregation pipeline array."},
			{Name: "upsert", DisplayName: "Insert if no match", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: updating},
			{Name: "returnDocument", DisplayName: "Return", Type: nodesdk.PropOptions, Default: returnAfter, DisplayOptions: nodekit.Show("operation", opFindOneUpdate),
				Options: []nodesdk.Option{nodekit.Opt(returnAfter, "Document after the update"), nodekit.Opt(returnBefore, "Document before the update")}},
			{Name: "multiple", DisplayName: "Affect all matches", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: nodekit.Show("operation", opUpdate, opDelete),
				Description: "Off changes only the first matching document."},
			{Name: "documents", DisplayName: "Documents", Type: nodesdk.PropJSON, DisplayOptions: nodekit.Show("operation", opInsert),
				Description: "A document or an array of documents. Empty inserts the input item's JSON."},
			{Name: "pipeline", DisplayName: "Pipeline", Type: nodesdk.PropJSON, Required: true, DisplayOptions: nodekit.Show("operation", opAggregate),
				Description: "Array of aggregation stages. Results are capped by nodes.database.mongoMaxResults."},
		},
	}
}

// Execute implements nodesdk.Node. Every item is parsed before the first command runs.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	if n.clients == nil {
		return nodesdk.Output{}, fmt.Errorf("mongodb clients: %w", nodesdk.ErrUnavailable)
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	reqs := make([]request, len(items))
	for i := range items {
		if reqs[i], err = parse(ctx, in, op, i, n.maxResults); err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
	}
	cred, err := in.Credential(ctx, credmongo.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credmongo.Name, err)
	}
	client, dbName, err := n.clients.Mongo(ctx, cred)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("opening mongodb client: %w", err)
	}
	if dbName == "" {
		return nodesdk.Output{}, fmt.Errorf("credential %q has no database", credmongo.Name)
	}
	db := client.Database(dbName)
	var out []nodesdk.Item
	for i, r := range reqs {
		docs, err := run(ctx, db.Collection(r.collection), r)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		for _, d := range docs {
			out = append(out, nodekit.Paired(nodesdk.Item{JSON: d}, i))
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
