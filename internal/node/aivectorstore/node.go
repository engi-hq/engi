// Package aivectorstore implements the Vector Store node (engi.ai.vectorStore):
// insert, search and delete text chunks in a pgvector table of the user's
// PostgreSQL database. Texts are split by the optional Text Splitter sub-node
// and embedded by the Embeddings sub-node.
package aivectorstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/node/pgvector"
	"github.com/engi-hq/engi/internal/node/ragkit"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.vectorStore"

// Operations.
const (
	opInsert = "insert"
	opSearch = "search"
	opDelete = "delete"
)

// defaultTable is the default of the table parameter.
const defaultTable = "engi_documents"

// Config is what the node needs from the host.
type Config struct {
	// Pools hands out the shared Postgres pools. Nil makes Execute fail with
	// nodesdk.ErrUnavailable.
	Pools  sqlnode.Pools
	Limits ragkit.Limits
	// Rand is the entropy source of generated chunk ids; nil means crypto/rand.
	Rand io.Reader
}

// Node is the Vector Store node.
type Node struct{ cfg Config }

// New returns a Vector Store node.
func New(cfg Config) *Node {
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	return &Node{cfg: cfg}
}

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	ins, srch, del := nodekit.Show("operation", opInsert), nodekit.Show("operation", opSearch), nodekit.Show("operation", opDelete)
	props := []nodesdk.Property{
		{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opInsert, Options: []nodesdk.Option{
			nodekit.Opt(opInsert, "Insert documents"), nodekit.Opt(opSearch, "Search"), nodekit.Opt(opDelete, "Delete"),
		}},
	}
	props = append(props, ragkit.TableProperties(defaultTable)...)
	props = append(props,
		ragkit.DistanceProperty(nodekit.Show("operation", opInsert, opSearch)),
		nodesdk.Property{Name: "text", DisplayName: "Text", Type: nodesdk.PropString, Required: true, Default: "={{ $json.text }}", DisplayOptions: ins,
			Description: "The text to store. With a Text Splitter connected it is cut into chunks first."},
		nodesdk.Property{Name: "metadata", DisplayName: "Metadata", Type: nodesdk.PropJSON, DisplayOptions: ins,
			Description: "Optional JSON object stored with every chunk and usable as a search filter. chunkIndex and chunkCount are added unless set here."},
		nodesdk.Property{Name: "id", DisplayName: "Document ID", Type: nodesdk.PropString, DisplayOptions: ins,
			Description: "Optional. With an id, inserting again replaces the chunks (id for one chunk, id#0, id#1, ... for several). Empty generates a random id per chunk."},
		nodesdk.Property{Name: "createIfMissing", DisplayName: "Create table if missing", Type: nodesdk.PropBoolean, DisplayOptions: ins,
			Description: "Run CREATE EXTENSION IF NOT EXISTS vector and CREATE TABLE IF NOT EXISTS before inserting. The vector size is taken from the embedding model. Needs the privilege to create extensions on first use."},
		nodesdk.Property{Name: "createIndex", DisplayName: "Create HNSW index", Type: nodesdk.PropBoolean, DisplayOptions: ins,
			Description: "With \"Create table if missing\", also create an HNSW index for the chosen distance (pgvector supports at most 2000 dimensions)."},
		nodesdk.Property{Name: "query", DisplayName: "Query", Type: nodesdk.PropString, Required: true, DisplayOptions: srch,
			Description: "The text to look for. It is embedded with the connected model."},
		ragkit.TopKProperty(srch),
		ragkit.FilterProperty(nodekit.Show("operation", opSearch, opDelete)),
		nodesdk.Property{Name: "ids", DisplayName: "Chunk IDs", Type: nodesdk.PropString, DisplayOptions: del,
			Description: "Comma-separated chunk ids (or a list from an expression). Combined with the filter, a chunk must match both."},
	)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Vector Store", Group: nodesdk.GroupAI, Icon: "database",
		Description: "Insert, search and delete text chunks in a pgvector table of your PostgreSQL database.",
		Inputs: []nodesdk.PortSpec{
			nodekit.Main("main", ""),
			{Name: "embedding", Kind: nodesdk.PortAIEmbedding, DisplayName: "Embedding", Required: true, MaxConnections: 1},
			{Name: "textSplitter", Kind: nodesdk.PortAITextSplitter, DisplayName: "Text splitter", MaxConnections: 1},
		},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{ragkit.PostgresCredential()},
		Properties:  props,
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
	table, err := ragkit.ReadTable(ctx, in, 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	var run func(context.Context, nodesdk.Input, pgvector.Table, int) ([]nodesdk.Item, error)
	switch op {
	case opInsert:
		run = n.insert
	case opSearch:
		run = n.search
	case opDelete:
		run = n.delete
	default:
		return nodesdk.Output{}, fmt.Errorf("unknown operation %q", op)
	}
	out, err := run(ctx, in, table, len(items))
	if err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// chunkRef remembers which input item a chunk came from.
type chunkRef struct {
	item int
	row  pgvector.Row
}

func (n *Node) insert(ctx context.Context, in nodesdk.Input, table pgvector.Table, count int) ([]nodesdk.Item, error) {
	embedder, err := ragkit.Embedder(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	splitter, err := ragkit.Splitter(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	dist, err := ragkit.ReadDistance(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	create, err := nodesdk.ParamBool(ctx, in, "createIfMissing", 0)
	if err != nil {
		return nil, err
	}
	index, err := nodesdk.ParamBool(ctx, in, "createIndex", 0)
	if err != nil {
		return nil, err
	}
	var chunks []chunkRef
	var texts []string
	perItem := make([][]string, count)
	for i := range count {
		text, err := nodesdk.ParamString(ctx, in, "text", i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		meta, err := ragkit.JSONObject(ctx, in, "metadata", i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		id, err := nodesdk.ParamString(ctx, in, "id", i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		parts := []string{text}
		if splitter != nil {
			parts = splitter.Split(text)
		}
		if text == "" || len(parts) == 0 {
			return nil, nodekit.ItemError(i, errors.New("text is empty"))
		}
		for c, part := range parts {
			cid, err := n.chunkID(id, c, len(parts))
			if err != nil {
				return nil, nodekit.ItemError(i, err)
			}
			m := maps.Clone(meta)
			if m == nil {
				m = map[string]any{}
			}
			setDefault(m, "chunkIndex", float64(c))
			setDefault(m, "chunkCount", float64(len(parts)))
			chunks = append(chunks, chunkRef{item: i, row: pgvector.Row{ID: cid, Content: part, Metadata: m, Document: id}})
			texts = append(texts, part)
			perItem[i] = append(perItem[i], cid)
		}
	}
	res, err := embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embedding texts: %w", err)
	}
	rows := make([]pgvector.Row, len(chunks))
	for k, c := range chunks {
		c.row.Vector = res.Vectors[k]
		rows[k] = c.row
	}
	db, err := ragkit.DB(ctx, in, n.cfg.Pools)
	if err != nil {
		return nil, err
	}
	_, err = pgvector.Insert(ctx, db, table, rows, pgvector.InsertOptions{
		BatchSize: n.cfg.Limits.InsertBatchSize, Create: create, Index: create && index, Distance: dist,
	})
	if err != nil {
		return nil, fmt.Errorf("inserting into %s: %w", table.Name, err)
	}
	out := make([]nodesdk.Item, count)
	for i := range count {
		ids := make([]any, len(perItem[i]))
		for k, id := range perItem[i] {
			ids[k] = id
		}
		out[i] = nodekit.Paired(nodesdk.Item{JSON: map[string]any{"ids": ids, "chunks": float64(len(ids))}}, i)
	}
	return out, nil
}

func (n *Node) search(ctx context.Context, in nodesdk.Input, table pgvector.Table, count int) ([]nodesdk.Item, error) {
	embedder, err := ragkit.Embedder(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	dist, err := ragkit.ReadDistance(ctx, in, 0)
	if err != nil {
		return nil, err
	}
	queries := make([]string, count)
	topKs := make([]int, count)
	filters := make([]map[string]any, count)
	for i := range count {
		if queries[i], err = nodesdk.ParamString(ctx, in, "query", i); err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		if queries[i] == "" {
			return nil, nodekit.ItemError(i, errors.New("query is empty"))
		}
		if topKs[i], err = ragkit.TopK(ctx, in, i, n.cfg.Limits.MaxTopK); err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		if filters[i], err = ragkit.JSONObject(ctx, in, "filter", i); err != nil {
			return nil, nodekit.ItemError(i, err)
		}
	}
	res, err := embedder.Embed(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("embedding queries: %w", err)
	}
	db, err := ragkit.DB(ctx, in, n.cfg.Pools)
	if err != nil {
		return nil, err
	}
	var out []nodesdk.Item
	for i := range count {
		matches, err := pgvector.Search(ctx, db, table, res.Vectors[i], topKs[i], filters[i], dist)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		for _, m := range matches {
			out = append(out, nodekit.Paired(nodesdk.Item{JSON: map[string]any{
				"id": m.ID, "text": m.Content, "metadata": m.Metadata, "score": m.Score, "distance": m.Distance,
			}}, i))
		}
	}
	return out, nil
}

func (n *Node) delete(ctx context.Context, in nodesdk.Input, table pgvector.Table, count int) ([]nodesdk.Item, error) {
	db, err := ragkit.DB(ctx, in, n.cfg.Pools)
	if err != nil {
		return nil, err
	}
	out := make([]nodesdk.Item, count)
	for i := range count {
		ids, err := nodekit.ParamStringList(ctx, in, "ids", i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		if len(ids) > n.cfg.Limits.MaxDeleteIDs {
			return nil, nodekit.ItemError(i, fmt.Errorf("at most %d ids can be deleted at once, got %d", n.cfg.Limits.MaxDeleteIDs, len(ids)))
		}
		filter, err := ragkit.JSONObject(ctx, in, "filter", i)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		deleted, err := pgvector.Delete(ctx, db, table, ids, filter)
		if err != nil {
			return nil, nodekit.ItemError(i, err)
		}
		out[i] = nodekit.Paired(nodesdk.Item{JSON: map[string]any{"deleted": float64(deleted)}}, i)
	}
	return out, nil
}

// chunkID returns the id of chunk c of total: the caller's id (suffixed with
// #c when there are several chunks) or a random UUID.
func (n *Node) chunkID(id string, c, total int) (string, error) {
	switch {
	case id == "":
		return newUUID(n.cfg.Rand)
	case total == 1:
		return id, nil
	}
	return id + "#" + strconv.Itoa(c), nil
}

func setDefault(m map[string]any, key string, v any) {
	if _, ok := m[key]; !ok {
		m[key] = v
	}
}

// newUUID returns a random (version 4) UUID read from r.
func newUUID(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generating id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
