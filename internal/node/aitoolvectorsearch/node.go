// Package aitoolvectorsearch implements the Vector Search tool sub-node
// (engi.ai.tool.vectorSearch). It gives an agent one tool that embeds a
// question, looks up the closest chunks in a pgvector table and returns them as
// JSON, so the agent can answer from your documents (retrieval-augmented
// generation).
package aitoolvectorsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/engi-hq/engi/internal/node/pgvector"
	"github.com/engi-hq/engi/internal/node/ragkit"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.tool.vectorSearch"

// Defaults of the name and description parameters.
const (
	defaultToolName    = "search_documents"
	defaultDescription = "Search the knowledge base for passages relevant to a question. Input is a natural-language query; the result lists the closest passages with their metadata and a similarity score."
	defaultTable       = "engi_documents"
)

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// Config is what the node needs from the host.
type Config struct {
	// Pools hands out the shared Postgres pools. Nil makes Supply fail with
	// nodesdk.ErrUnavailable.
	Pools  sqlnode.Pools
	Limits ragkit.Limits
}

// Node is the Vector Search tool sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns a Vector Search tool node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	props := []nodesdk.Property{
		{Name: "name", DisplayName: "Tool name", Type: nodesdk.PropString, Required: true, Default: defaultToolName,
			Description: "What the model calls the tool: letters, digits, underscores and hyphens (at most 64). Use distinct names when an agent has several knowledge bases."},
		{Name: "description", DisplayName: "Description", Type: nodesdk.PropString, Required: true, Default: defaultDescription,
			Description: "Tells the model when to use the tool. Say what the knowledge base contains."},
	}
	props = append(props, ragkit.TableProperties(defaultTable)...)
	props = append(props, ragkit.DistanceProperty(nil), ragkit.TopKProperty(nil), ragkit.FilterProperty(nil))
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Vector Search Tool", Group: nodesdk.GroupAI, Icon: "database",
		Description: "A tool that lets an agent search a pgvector table (the one the Vector Store node fills) for relevant passages.",
		Inputs:      []nodesdk.PortSpec{{Name: "embedding", Kind: nodesdk.PortAIEmbedding, DisplayName: "Embedding", Required: true, MaxConnections: 1}},
		Outputs:     []nodesdk.PortSpec{{Name: "tool", Kind: nodesdk.PortAITool}},
		Credentials: []nodesdk.CredentialRef{ragkit.PostgresCredential()},
		Properties:  props,
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("vector search tool: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ToolProvider.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	if n.cfg.Pools == nil {
		return nil, fmt.Errorf("database pools: %w", nodesdk.ErrUnavailable)
	}
	name, err := nodesdk.ParamString(ctx, in, "name", 0)
	if err != nil {
		return nil, err
	}
	if name = strings.TrimSpace(name); !toolNamePattern.MatchString(name) {
		return nil, fmt.Errorf("tool name %q must be 1-64 letters, digits, underscores or hyphens", name)
	}
	desc, err := nodesdk.ParamString(ctx, in, "description", 0)
	if err != nil {
		return nil, err
	}
	table, err := ragkit.ReadTable(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	dist, err := ragkit.ReadDistance(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	topK, err := ragkit.TopK(ctx, in, 0, n.cfg.Limits.MaxTopK)
	if err != nil {
		return nil, err
	}
	filter, err := ragkit.JSONObject(ctx, in, "filter", 0)
	if err != nil {
		return nil, err
	}
	embedder, err := ragkit.Embedder(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	cred, err := in.Credential(ctx, ragkit.PostgresCredential().Name)
	if err != nil {
		return nil, fmt.Errorf("loading credential %q: %w", ragkit.PostgresCredential().Name, err)
	}
	t := &searchTool{
		def:   port.ToolDefinition{Name: name, Description: desc, Parameters: querySchema},
		pools: n.cfg.Pools, cred: cred, table: table, dist: dist, topK: topK, filter: filter, embedder: embedder,
	}
	return provider{t}, nil
}

type provider struct{ tool port.Tool }

// Tools implements port.ToolProvider.
func (p provider) Tools(context.Context) ([]port.Tool, error) { return []port.Tool{p.tool}, nil }

var querySchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"What to look for, in natural language."}},"required":["query"],"additionalProperties":false}`)

// searchTool implements port.Tool.
type searchTool struct {
	def      port.ToolDefinition
	pools    sqlnode.Pools
	cred     nodesdk.Credential
	table    pgvector.Table
	dist     pgvector.Distance
	topK     int
	filter   map[string]any
	embedder port.Embedder
}

// Definition implements port.Tool.
func (t *searchTool) Definition() port.ToolDefinition { return t.def }

// Call implements port.Tool.
func (t *searchTool) Call(ctx context.Context, args json.RawMessage) (port.ToolResult, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return port.ToolResult{Content: "arguments must be a JSON object with a \"query\" string", IsError: true}, nil
	}
	if strings.TrimSpace(a.Query) == "" {
		return port.ToolResult{Content: "query must not be empty", IsError: true}, nil
	}
	res, err := t.embedder.Embed(ctx, []string{a.Query})
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("embedding query: %w", err)
	}
	if len(res.Vectors) != 1 {
		return port.ToolResult{}, errors.New("embedding query: no vector returned")
	}
	db, err := ragkit.OpenDB(ctx, t.pools, t.cred)
	if err != nil {
		return port.ToolResult{}, err
	}
	matches, err := pgvector.Search(ctx, db, t.table, res.Vectors[0], t.topK, t.filter, t.dist)
	if err != nil {
		return port.ToolResult{}, err
	}
	out := make([]map[string]any, len(matches))
	for i, m := range matches {
		out[i] = map[string]any{"id": m.ID, "text": m.Content, "metadata": m.Metadata, "score": m.Score}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return port.ToolResult{}, fmt.Errorf("encoding results: %w", err)
	}
	return port.ToolResult{Content: string(b)}, nil
}
