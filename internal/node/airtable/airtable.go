// Package airtable implements the Airtable node (engi.airtable) over the REST
// API with plain HTTP: list, get, create, update, upsert and delete records.
// Writes are sent in batches of at most nodes.airtable.batchSize records.
package airtable

import (
	"context"
	"fmt"

	credairtable "github.com/engi-hq/engi/internal/credtype/airtable"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.airtable"

// Operations.
const (
	opList   = "listRecords"
	opGet    = "getRecord"
	opCreate = "createRecords"
	opUpdate = "updateRecords"
	opUpsert = "upsertRecords"
	opDelete = "deleteRecords"
)

// Config is what the node needs from nodes.airtable.*.
type Config struct {
	BaseURL        string
	MaxListResults int
	PageSize       int
	BatchSize      int
	Caller         chatkit.Caller
}

// Node is the Airtable node.
type Node struct{ cfg Config }

// New returns an Airtable node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := [][2]string{
		{opList, "List records"}, {opGet, "Get record"}, {opCreate, "Create records"},
		{opUpdate, "Update records"}, {opUpsert, "Upsert records"}, {opDelete, "Delete records"},
	}
	opts := make([]nodesdk.Option, len(ops))
	for i, o := range ops {
		opts[i] = nodekit.Opt(o[0], o[1])
	}
	writeOps := nodekit.Show("operation", opCreate, opUpdate, opUpsert)
	idOps := nodekit.Show("operation", opGet, opUpdate, opDelete)
	list := nodekit.Show("operation", opList)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Airtable", Group: nodesdk.GroupAction, Icon: "table",
		Description: "List, get, create, update, upsert and delete Airtable records.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credairtable.Name, Type: credairtable.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opList, Options: opts},
			{Name: "baseId", DisplayName: "Base ID", Type: nodesdk.PropString, Required: true, Description: "The app... ID of the base."},
			{Name: "table", DisplayName: "Table", Type: nodesdk.PropString, Required: true, Description: "Table ID (tbl...) or name."},
			{Name: "recordId", DisplayName: "Record ID", Type: nodesdk.PropString, Required: true, DisplayOptions: idOps},
			{Name: "fields", DisplayName: "Fields (JSON)", Type: nodesdk.PropJSON, DisplayOptions: writeOps,
				Description: "Object of field name to value. Empty uses the input item's JSON."},
			{Name: "typecast", DisplayName: "Typecast", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: writeOps,
				Description: "Let Airtable convert strings to the field type and create missing select options."},
			{Name: "mergeOn", DisplayName: "Fields to merge on", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opUpsert),
				Description: "Comma separated field names (up to 3) that identify an existing record."},
			{Name: "filterByFormula", DisplayName: "Filter formula", Type: nodesdk.PropString, DisplayOptions: list},
			{Name: "view", DisplayName: "View", Type: nodesdk.PropString, DisplayOptions: list, Description: "View name or ID."},
			{Name: "returnFields", DisplayName: "Return only fields", Type: nodesdk.PropString, DisplayOptions: list, Description: "Comma separated field names. Empty returns all."},
			{Name: "sort", DisplayName: "Sort (JSON)", Type: nodesdk.PropJSON, DisplayOptions: list,
				Description: `Array like [{"field":"Name","direction":"asc"}].`},
			{Name: "limit", DisplayName: "Limit", Type: nodesdk.PropNumber, Default: 0, DisplayOptions: list,
				Description: "Max records to return. 0 returns up to the configured cap (nodes.airtable.maxListResults)."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	cred, err := in.Credential(ctx, credairtable.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credairtable.Name, err)
	}
	r := &runner{
		cfg: n.cfg, in: in,
		c: &client{cfg: n.cfg, http: in.HTTP(), cred: cred, token: chatkit.Secret(cred, "token")},
	}
	switch op {
	case opList, opGet:
		return r.perItem(ctx, op, items)
	case opCreate, opUpdate, opUpsert, opDelete:
		return r.batched(ctx, op, items)
	}
	return nodesdk.Output{}, fmt.Errorf("unknown operation %q", op)
}
