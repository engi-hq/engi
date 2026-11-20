// Package pgvector is the SQL layer of the vector store nodes: statement
// builders for a pgvector table plus Insert, Search and Delete that run them on
// a database/sql handle (a pool from port.DBPool, handed in by the node).
//
// Table layout (one table per collection, see docs/14-ai.md):
//
//	id        text primary key        caller supplied or generated
//	content   text not null           the chunk text
//	metadata  jsonb not null          free-form object, filtered with @>
//	embedding vector(N) not null      N is fixed when the table is created
//
// User data only ever travels as bound parameters. Table and schema names are
// restricted to plain identifiers and still quoted. Vectors and JSON are bound
// as text and cast on the server ($1::text::vector), so no driver-specific
// vector type is needed.
package pgvector

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Distance is the metric a table is searched with.
type Distance string

// Distance metrics, matching the pgvector operators.
const (
	// Cosine uses <=> (cosine distance); the score is the cosine similarity.
	Cosine Distance = "cosine"
	// L2 uses <-> (Euclidean distance); the score is 1/(1+distance).
	L2 Distance = "l2"
	// InnerProduct uses <#> (negative inner product); the score is the inner product.
	InnerProduct Distance = "innerProduct"
)

// MaxIdentLen bounds table and schema names. It leaves room for the suffix of
// the index name inside PostgreSQL's 63-byte identifier limit.
const MaxIdentLen = 48

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrInvalidIdentifier is returned for a table or schema name that is not a
// plain identifier.
var ErrInvalidIdentifier = errors.New("invalid identifier")

// Table names a pgvector table. Schema may be empty (search path).
type Table struct {
	Schema string
	Name   string
}

// Validate checks both names.
func (t Table) Validate() error {
	if err := checkIdent(t.Name); err != nil {
		return fmt.Errorf("table name %q: %w", t.Name, err)
	}
	if t.Schema != "" {
		if err := checkIdent(t.Schema); err != nil {
			return fmt.Errorf("schema name %q: %w", t.Schema, err)
		}
	}
	return nil
}

func checkIdent(s string) error {
	if s == "" || len(s) > MaxIdentLen || !identPattern.MatchString(s) {
		return fmt.Errorf("%w: use letters, digits and underscores, starting with a letter or underscore, at most %d characters", ErrInvalidIdentifier, MaxIdentLen)
	}
	return nil
}

func quote(s string) string { return `"` + s + `"` }

// SQL returns the quoted, schema-qualified name.
func (t Table) SQL() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	if t.Schema == "" {
		return quote(t.Name), nil
	}
	return quote(t.Schema) + "." + quote(t.Name), nil
}

// Statement is one parameterised SQL statement.
type Statement struct {
	SQL  string
	Args []any
}

// Row is one stored chunk.
type Row struct {
	ID       string
	Content  string
	Metadata map[string]any
	Vector   []float32
	// Document is the caller's document id when this row is a chunk of a
	// document (ID is then the document id or document id plus "#n"). Insert
	// first deletes the document's earlier chunks, so a re-ingested document
	// never keeps stale chunks. Empty means the row is only upserted by ID.
	Document string
}

// Match is one search result.
type Match struct {
	ID       string
	Content  string
	Metadata map[string]any
	// Distance is the raw pgvector distance (smaller is closer).
	Distance float64
	// Score grows with similarity (see the Distance constants).
	Score float64
}

// Valid reports whether d is a known metric.
func (d Distance) Valid() bool { return d == Cosine || d == L2 || d == InnerProduct }

func (d Distance) operator() string {
	switch d {
	case L2:
		return "<->"
	case InnerProduct:
		return "<#>"
	default:
		return "<=>"
	}
}

func (d Distance) opClass() string {
	switch d {
	case L2:
		return "vector_l2_ops"
	case InnerProduct:
		return "vector_ip_ops"
	default:
		return "vector_cosine_ops"
	}
}

// ScoreOf converts a raw distance into the score of the metric.
func (d Distance) ScoreOf(dist float64) float64 {
	switch d {
	case L2:
		return 1 / (1 + dist)
	case InnerProduct:
		return -dist
	default:
		return 1 - dist
	}
}

// Literal formats a vector as the pgvector text form "[1,2,3]". It rejects an
// empty vector and NaN or infinite components.
func Literal(v []float32) (string, error) {
	if len(v) == 0 {
		return "", errors.New("empty vector")
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return "", fmt.Errorf("vector component %d is not a finite number", i)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

// CreateExtensionSQL enables the pgvector extension.
const CreateExtensionSQL = "CREATE EXTENSION IF NOT EXISTS vector"

// CreateTableSQL creates the table for vectors of dims dimensions.
func CreateTableSQL(t Table, dims int) (string, error) {
	name, err := t.SQL()
	if err != nil {
		return "", err
	}
	if dims < 1 {
		return "", errors.New("dimensions must be at least 1")
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id text PRIMARY KEY, content text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}'::jsonb, embedding vector(%d) NOT NULL)",
		name, dims), nil
}

// CreateIndexSQL creates an HNSW index matching the metric. pgvector indexes
// at most 2000 dimensions; the server reports the error for larger vectors.
func CreateIndexSQL(t Table, d Distance) (string, error) {
	name, err := t.SQL()
	if err != nil {
		return "", err
	}
	if !d.Valid() {
		return "", fmt.Errorf("unknown distance %q", d)
	}
	return fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding %s)", quote(t.Name+"_embedding_idx"), name, d.opClass()), nil
}

// UpsertStatements builds INSERT .. ON CONFLICT (id) DO UPDATE statements of at
// most batch rows each. Every row needs an id and a vector of the same length.
func UpsertStatements(t Table, rows []Row, batch int) ([]Statement, error) {
	name, err := t.SQL()
	if err != nil {
		return nil, err
	}
	if batch < 1 {
		return nil, errors.New("batch size must be at least 1")
	}
	var dims int
	for i, r := range rows {
		if r.ID == "" {
			return nil, fmt.Errorf("row %d has no id", i)
		}
		if i == 0 {
			dims = len(r.Vector)
		}
		if len(r.Vector) != dims {
			return nil, fmt.Errorf("row %d has %d dimensions, row 0 has %d", i, len(r.Vector), dims)
		}
	}
	var out []Statement
	for start := 0; start < len(rows); start += batch {
		end := min(start+batch, len(rows))
		var sb strings.Builder
		sb.WriteString("INSERT INTO " + name + " (id, content, metadata, embedding) VALUES ")
		args := make([]any, 0, (end-start)*4)
		for i, r := range rows[start:end] {
			meta, err := metadataJSON(r.Metadata)
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", start+i, err)
			}
			vec, err := Literal(r.Vector)
			if err != nil {
				return nil, fmt.Errorf("row %d: %w", start+i, err)
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			n := len(args)
			fmt.Fprintf(&sb, "($%d, $%d, $%d::text::jsonb, $%d::text::vector)", n+1, n+2, n+3, n+4)
			args = append(args, r.ID, r.Content, meta, vec)
		}
		sb.WriteString(" ON CONFLICT (id) DO UPDATE SET content = EXCLUDED.content, metadata = EXCLUDED.metadata, embedding = EXCLUDED.embedding")
		out = append(out, Statement{SQL: sb.String(), Args: args})
	}
	return out, nil
}

// SearchStatement builds the nearest-neighbour query. filter, when not empty,
// keeps only rows whose metadata contains it (jsonb @>). The statement selects
// id, content, metadata (as text) and distance.
func SearchStatement(t Table, query []float32, topK int, filter map[string]any, d Distance) (Statement, error) {
	name, err := t.SQL()
	if err != nil {
		return Statement{}, err
	}
	if !d.Valid() {
		return Statement{}, fmt.Errorf("unknown distance %q", d)
	}
	if topK < 1 {
		return Statement{}, errors.New("topK must be at least 1")
	}
	vec, err := Literal(query)
	if err != nil {
		return Statement{}, fmt.Errorf("query vector: %w", err)
	}
	dist := "embedding " + d.operator() + " $1::text::vector"
	args := []any{vec}
	where := ""
	if len(filter) > 0 {
		f, err := metadataJSON(filter)
		if err != nil {
			return Statement{}, fmt.Errorf("filter: %w", err)
		}
		args = append(args, f)
		where = " WHERE metadata @> $2::text::jsonb"
	}
	args = append(args, topK)
	sql := fmt.Sprintf("SELECT id, content, metadata::text, %s AS distance FROM %s%s ORDER BY %s LIMIT $%d", dist, name, where, dist, len(args))
	return Statement{SQL: sql, Args: args}, nil
}

// DocumentDeleteStatement deletes the chunks of one document: the row with id
// docID and the rows docID#0, docID#1, ... that Insert writes for a document
// split into several chunks. Other ids that merely start with docID (docID2,
// docID#x) are kept. The match is by id prefix, so PostgreSQL scans the table
// (there is no index on the document key).
func DocumentDeleteStatement(t Table, docID string) (Statement, error) {
	name, err := t.SQL()
	if err != nil {
		return Statement{}, err
	}
	if docID == "" {
		return Statement{}, errors.New("document id must not be empty")
	}
	prefix := docID + "#"
	sql := "DELETE FROM " + name + " WHERE id = $1::text OR (left(id, length($2::text)) = $2::text AND substr(id, length($2::text) + 1) ~ '^[0-9]+$')"
	return Statement{SQL: sql, Args: []any{docID, prefix}}, nil
}

// DeleteStatement builds a DELETE by ids, by metadata filter, or both (rows
// must match an id and the filter). It refuses to delete without a criterion.
func DeleteStatement(t Table, ids []string, filter map[string]any) (Statement, error) {
	name, err := t.SQL()
	if err != nil {
		return Statement{}, err
	}
	if len(ids) == 0 && len(filter) == 0 {
		return Statement{}, errors.New("delete needs ids or a metadata filter")
	}
	var conds []string
	var args []any
	if len(ids) > 0 {
		ph := make([]string, len(ids))
		for i, id := range ids {
			args = append(args, id)
			ph[i] = "$" + strconv.Itoa(len(args))
		}
		conds = append(conds, "id IN ("+strings.Join(ph, ", ")+")")
	}
	if len(filter) > 0 {
		f, err := metadataJSON(filter)
		if err != nil {
			return Statement{}, fmt.Errorf("filter: %w", err)
		}
		args = append(args, f)
		conds = append(conds, fmt.Sprintf("metadata @> $%d::text::jsonb", len(args)))
	}
	return Statement{SQL: "DELETE FROM " + name + " WHERE " + strings.Join(conds, " AND "), Args: args}, nil
}
