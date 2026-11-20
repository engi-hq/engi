// Package ragkit holds what the vector store node and the vector search tool
// share: the Postgres connection through the injected pools, the table and
// distance parameters, and pulling the embedder and splitter sub-ports.
package ragkit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	credpostgres "github.com/engi-hq/engi/internal/credtype/postgres"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/node/pgvector"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Limits are the host-configured bounds of the retrieval nodes
// (nodes.ai.rag.*).
type Limits struct {
	MaxTopK         int
	InsertBatchSize int
	MaxDeleteIDs    int
}

// PostgresCredential is the descriptor entry both nodes use.
func PostgresCredential() nodesdk.CredentialRef {
	return nodesdk.CredentialRef{Name: credpostgres.Name, Type: credpostgres.Name, Required: true}
}

// Distances are the options of the distance parameter.
func Distances() []nodesdk.Option {
	return []nodesdk.Option{
		nodekit.Opt(string(pgvector.Cosine), "Cosine"),
		nodekit.Opt(string(pgvector.L2), "Euclidean (L2)"),
		nodekit.Opt(string(pgvector.InnerProduct), "Inner product"),
	}
}

// DistanceProperty is the distance parameter.
func DistanceProperty(show *nodesdk.DisplayOptions) nodesdk.Property {
	return nodesdk.Property{Name: "distance", DisplayName: "Distance", Type: nodesdk.PropOptions, Default: string(pgvector.Cosine),
		Options: Distances(), DisplayOptions: show,
		Description: "Metric used to rank results (and for the index created with the table). Must match how you want similarity measured; it is not stored in the table."}
}

// TableProperties are the schema and table parameters.
func TableProperties(defaultTable string) []nodesdk.Property {
	return []nodesdk.Property{
		{Name: "schema", DisplayName: "Schema", Type: nodesdk.PropString,
			Description: "Optional. Empty uses the connection's search path (usually public)."},
		{Name: "table", DisplayName: "Table", Type: nodesdk.PropString, Required: true, Default: defaultTable,
			Description: "Table that holds the vectors (letters, digits and underscores)."},
	}
}

// ReadTable reads and validates the schema and table parameters.
func ReadTable(ctx context.Context, in nodesdk.Input, item int) (pgvector.Table, error) {
	schema, err := nodesdk.ParamString(ctx, in, "schema", item)
	if err != nil {
		return pgvector.Table{}, err
	}
	name, err := nodesdk.ParamString(ctx, in, "table", item)
	if err != nil {
		return pgvector.Table{}, err
	}
	t := pgvector.Table{Schema: schema, Name: name}
	if err := t.Validate(); err != nil {
		return pgvector.Table{}, err //nolint:wrapcheck // already descriptive
	}
	return t, nil
}

// ReadDistance reads the distance parameter.
func ReadDistance(ctx context.Context, in nodesdk.Input, item int) (pgvector.Distance, error) {
	s, err := nodesdk.ParamString(ctx, in, "distance", item)
	if err != nil {
		return "", err
	}
	if s == "" {
		return pgvector.Cosine, nil
	}
	d := pgvector.Distance(s)
	if !d.Valid() {
		return "", fmt.Errorf("unknown distance %q", s)
	}
	return d, nil
}

// DB opens the shared pool for the Postgres credential bound to the node.
func DB(ctx context.Context, in nodesdk.Input, pools sqlnode.Pools) (*sql.DB, error) {
	if pools == nil {
		return nil, fmt.Errorf("database pools: %w", nodesdk.ErrUnavailable)
	}
	cred, err := in.Credential(ctx, credpostgres.Name)
	if err != nil {
		return nil, fmt.Errorf("loading credential %q: %w", credpostgres.Name, err)
	}
	return OpenDB(ctx, pools, cred)
}

// OpenDB opens the shared pool for an already loaded Postgres credential.
func OpenDB(ctx context.Context, pools sqlnode.Pools, cred nodesdk.Credential) (*sql.DB, error) {
	db, err := pools.DB(ctx, port.DriverPostgres, cred)
	if err != nil {
		return nil, fmt.Errorf("opening postgres pool: %w", err)
	}
	return db, nil
}

// Embedder pulls the single connected ai.embedding sub-node.
func Embedder(ctx context.Context, in nodesdk.Input, item int) (port.Embedder, error) {
	got, err := in.SubPort(ctx, nodesdk.PortAIEmbedding, item)
	if err != nil {
		return nil, fmt.Errorf("embedding input: %w", err)
	}
	if len(got) == 0 {
		return nil, errors.New("connect an Embeddings node to the embedding input")
	}
	e, ok := got[0].Value.(port.Embedder)
	if !ok {
		return nil, fmt.Errorf("sub-node %q supplied %T, not an embedder", got[0].Node, got[0].Value)
	}
	return e, nil
}

// Splitter pulls the optional ai.textSplitter sub-node; nil when none is connected.
func Splitter(ctx context.Context, in nodesdk.Input, item int) (port.TextSplitter, error) {
	got, err := in.SubPort(ctx, nodesdk.PortAITextSplitter, item)
	if err != nil {
		return nil, fmt.Errorf("text splitter input: %w", err)
	}
	if len(got) == 0 {
		return nil, nil
	}
	s, ok := got[0].Value.(port.TextSplitter)
	if !ok {
		return nil, fmt.Errorf("sub-node %q supplied %T, not a text splitter", got[0].Node, got[0].Value)
	}
	return s, nil
}

// JSONObject reads a metadata or filter parameter: an object, JSON text of one,
// or nothing (an empty object).
func JSONObject(ctx context.Context, in nodesdk.Input, name string, item int) (map[string]any, error) {
	v, err := in.Param(ctx, name, item)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return t, nil
	case string:
		if t == "" {
			return map[string]any{}, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err != nil || m == nil {
			return nil, fmt.Errorf("parameter %q must be a JSON object", name)
		}
		return m, nil
	}
	return nil, fmt.Errorf("parameter %q must be a JSON object, got %s", name, nodekit.TypeName(v))
}

// TopK reads and bounds the topK parameter.
func TopK(ctx context.Context, in nodesdk.Input, item, maxTopK int) (int, error) {
	k, err := nodesdk.ParamInt(ctx, in, "topK", item)
	if err != nil {
		return 0, err
	}
	if k < 1 || k > maxTopK {
		return 0, fmt.Errorf("topK must be between 1 and %d", maxTopK)
	}
	return k, nil
}

// TopKProperty is the topK parameter.
func TopKProperty(show *nodesdk.DisplayOptions) nodesdk.Property {
	return nodesdk.Property{Name: "topK", DisplayName: "Top K", Type: nodesdk.PropNumber, Default: float64(DefaultTopK), DisplayOptions: show,
		Description: "Number of closest chunks to return (at most nodes.ai.rag.maxTopK)."}
}

// FilterProperty is the metadata filter parameter.
func FilterProperty(show *nodesdk.DisplayOptions) nodesdk.Property {
	return nodesdk.Property{Name: "filter", DisplayName: "Metadata filter", Type: nodesdk.PropJSON, DisplayOptions: show,
		Description: "Optional JSON object. Only chunks whose metadata contains it are matched, for example {\"source\": \"handbook\"}."}
}

// DefaultTopK is the default of the topK parameter.
const DefaultTopK = 4
