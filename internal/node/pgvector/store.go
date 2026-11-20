package pgvector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Querier is what *sql.DB and *sql.Tx share.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Beginner starts transactions (*sql.DB).
type Beginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// InsertOptions control Insert.
type InsertOptions struct {
	// BatchSize is the rows per INSERT statement.
	BatchSize int
	// Create creates the extension and table (and the index when Index is set)
	// before inserting. The table's dimensions are those of the first row.
	Create bool
	// Index creates an HNSW index for Distance (only with Create).
	Index    bool
	Distance Distance
}

// Insert upserts rows in one transaction and returns how many it wrote. For
// every distinct Row.Document it first deletes that document's existing chunks,
// in the same transaction, so a document that shrank leaves no stale chunks.
func Insert(ctx context.Context, db Beginner, t Table, rows []Row, o InsertOptions) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	stmts, err := UpsertStatements(t, rows, o.BatchSize)
	if err != nil {
		return 0, err
	}
	var setup []string
	if o.Create {
		ddl, err := CreateTableSQL(t, len(rows[0].Vector))
		if err != nil {
			return 0, err
		}
		setup = append(setup, CreateExtensionSQL, ddl)
		if o.Index {
			idx, err := CreateIndexSQL(t, o.Distance)
			if err != nil {
				return 0, err
			}
			setup = append(setup, idx)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range setup {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return 0, fmt.Errorf("preparing vector table: %w", err)
		}
	}
	for _, doc := range documentIDs(rows) {
		st, err := DocumentDeleteStatement(t, doc)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, st.SQL, st.Args...); err != nil {
			return 0, fmt.Errorf("replacing the chunks of document %q: %w%s", doc, err, hint(err))
		}
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.SQL, s.Args...); err != nil {
			return 0, fmt.Errorf("inserting vectors: %w%s", err, hint(err))
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing: %w", err)
	}
	return len(rows), nil
}

// Search returns the topK nearest rows, closest first.
func Search(ctx context.Context, q Querier, t Table, query []float32, topK int, filter map[string]any, d Distance) ([]Match, error) {
	st, err := SearchStatement(t, query, topK, filter, d)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, st.SQL, st.Args...)
	if err != nil {
		return nil, fmt.Errorf("searching vectors: %w%s", err, hint(err))
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		var meta string
		if err := rows.Scan(&m.ID, &m.Content, &meta, &m.Distance); err != nil {
			return nil, fmt.Errorf("reading search result: %w", err)
		}
		if err := json.Unmarshal([]byte(meta), &m.Metadata); err != nil {
			return nil, fmt.Errorf("decoding metadata of %q: %w", m.ID, err)
		}
		m.Score = d.ScoreOf(m.Distance)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading search results: %w", err)
	}
	return out, nil
}

// Delete removes rows by ids and/or metadata filter and returns how many.
func Delete(ctx context.Context, q Querier, t Table, ids []string, filter map[string]any) (int64, error) {
	st, err := DeleteStatement(t, ids, filter)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, st.SQL, st.Args...)
	if err != nil {
		return 0, fmt.Errorf("deleting vectors: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting deleted rows: %w", err)
	}
	return n, nil
}

// documentIDs returns the distinct non-empty Document values, in row order.
func documentIDs(rows []Row) []string {
	seen := make(map[string]bool, len(rows))
	var out []string
	for _, r := range rows {
		if r.Document != "" && !seen[r.Document] {
			seen[r.Document] = true
			out = append(out, r.Document)
		}
	}
	return out
}

// metadataJSON encodes a metadata object ("{}" for nil).
func metadataJSON(m map[string]any) (string, error) {
	if m == nil {
		m = map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding metadata: %w", err)
	}
	return string(b), nil
}

// hint adds advice for the two failures users hit most.
func hint(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "dimensions"):
		return " (the table was created for a different embedding size; use the same model and dimensions, or another table)"
	case strings.Contains(msg, `type "vector" does not exist`), strings.Contains(msg, "extension"):
		return " (is the pgvector extension installed? enable \"Create table if missing\" or run CREATE EXTENSION vector)"
	}
	return ""
}
