// Package redis implements the Redis node (engi.db.redis): get, set, delete,
// incr/decr, keys, hash get/set, list push/pop and publish.
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	credredis "github.com/engi-hq/engi/internal/credtype/redis"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.db.redis"

// Clients hands out shared Redis clients (satisfied by redispool.Manager).
type Clients interface {
	Redis(ctx context.Context, cred nodesdk.Credential) (*goredis.Client, error)
}

// Operations.
const (
	opGet      = "get"
	opSet      = "set"
	opDelete   = "delete"
	opIncr     = "incr"
	opDecr     = "decr"
	opKeys     = "keys"
	opHashGet  = "hashGet"
	opHashSet  = "hashSet"
	opListPush = "listPush"
	opListPop  = "listPop"
	opPublish  = "publish"
)

// Sides of a list.
const (
	sideLeft  = "left"
	sideRight = "right"
)

// Node is the Redis node.
type Node struct {
	clients Clients
	maxKeys int
}

// New returns a Redis node. maxKeys caps the keys operation (nodes.database.redisMaxKeys).
// Without clients Execute fails with nodesdk.ErrUnavailable.
func New(clients Clients, maxKeys int) *Node { return &Node{clients: clients, maxKeys: maxKeys} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ops := [][2]string{
		{opGet, "Get"}, {opSet, "Set"}, {opDelete, "Delete"}, {opIncr, "Increment"}, {opDecr, "Decrement"}, {opKeys, "List keys"},
		{opHashGet, "Hash: get"}, {opHashSet, "Hash: set"}, {opListPush, "List: push"}, {opListPop, "List: pop"}, {opPublish, "Publish"},
	}
	opts := make([]nodesdk.Option, len(ops))
	for i, o := range ops {
		opts[i] = nodekit.Opt(o[0], o[1])
	}
	keyed := nodekit.Show("operation", opGet, opSet, opDelete, opIncr, opDecr, opHashGet, opHashSet, opListPush, opListPop)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Redis", Group: nodesdk.GroupAction, Icon: "database",
		Description: "Read and write Redis keys, hashes and lists, list keys, and publish messages.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: credredis.Name, Type: credredis.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opGet, Options: opts},
			{Name: "key", DisplayName: "Key", Type: nodesdk.PropString, Required: true, DisplayOptions: keyed},
			{Name: "value", DisplayName: "Value", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opSet, opListPush),
				Description: "Strings are stored as is; objects and arrays are stored as JSON."},
			{Name: "ttl", DisplayName: "Expire after (seconds)", Type: nodesdk.PropNumber, Default: float64(0), DisplayOptions: nodekit.Show("operation", opSet),
				Description: "0 keeps the key until it is deleted."},
			{Name: "amount", DisplayName: "Amount", Type: nodesdk.PropNumber, Default: float64(1), DisplayOptions: nodekit.Show("operation", opIncr, opDecr)},
			{Name: "pattern", DisplayName: "Pattern", Type: nodesdk.PropString, Default: "*", DisplayOptions: nodekit.Show("operation", opKeys),
				Description: "Glob pattern, e.g. user:*. Results are capped by nodes.database.redisMaxKeys."},
			{Name: "field", DisplayName: "Field", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opHashGet),
				Description: "Empty returns every field of the hash."},
			{Name: "fields", DisplayName: "Fields", Type: nodesdk.PropJSON, DisplayOptions: nodekit.Show("operation", opHashSet),
				Description: "Object of field names to values, e.g. {\"name\": \"Ada\"}."},
			{Name: "side", DisplayName: "End of list", Type: nodesdk.PropOptions, Default: sideLeft, DisplayOptions: nodekit.Show("operation", opListPush, opListPop),
				Options: []nodesdk.Option{nodekit.Opt(sideLeft, "Left (head)"), nodekit.Opt(sideRight, "Right (tail)")}},
			{Name: "channel", DisplayName: "Channel", Type: nodesdk.PropString, Required: true, DisplayOptions: nodekit.Show("operation", opPublish)},
			{Name: "message", DisplayName: "Message", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("operation", opPublish)},
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
		return nodesdk.Output{}, fmt.Errorf("redis clients: %w", nodesdk.ErrUnavailable)
	}
	op, err := nodesdk.ParamString(ctx, in, "operation", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	reqs := make([]request, len(items))
	for i := range items {
		if reqs[i], err = parse(ctx, in, op, i); err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
	}
	cred, err := in.Credential(ctx, credredis.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credredis.Name, err)
	}
	rdb, err := n.clients.Redis(ctx, cred)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("opening redis client: %w", err)
	}
	out := make([]nodesdk.Item, 0, len(items))
	for i, r := range reqs {
		res, err := run(ctx, rdb, r, n.maxKeys)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
