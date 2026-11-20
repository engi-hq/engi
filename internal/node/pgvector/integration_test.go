//go:build integration

package pgvector_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/engi-hq/engi/internal/node/pgvector"
)

// dsnEnv optionally names a PostgreSQL DSN of a server with the pgvector
// extension available. When it is unset the tests start a throwaway pgvector
// container with Docker, shared by the tests of the process.
const dsnEnv = "ENGI_TEST_PGVECTOR_DSN"

// pgvectorImage is the container image of the live tests (PostgreSQL 17 with
// the vector extension preinstalled).
const pgvectorImage = "pgvector/pgvector:pg17"

// containerStartup bounds the start of the container (image pull included).
const containerStartup = 5 * time.Minute

var (
	containerOnce sync.Once
	containerDSN  string
	containerErr  error
)

// liveDSN returns the DSN the live tests use.
func liveDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv(dsnEnv); dsn != "" {
		return dsn
	}
	containerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), containerStartup)
		defer cancel()
		c, err := tcpostgres.Run(ctx, pgvectorImage,
			tcpostgres.WithDatabase("engi"), tcpostgres.WithUsername("engi"), tcpostgres.WithPassword("secret"),
			tcpostgres.BasicWaitStrategies())
		if err != nil {
			containerErr = fmt.Errorf("starting pgvector container: %w", err)
			return
		}
		containerDSN, containerErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	if containerErr != nil {
		t.Fatal(containerErr)
	}
	return containerDSN
}

// openLive opens a pool on the live database and a fresh table that the test
// drops when it ends.
func openLive(t *testing.T) (*sql.DB, pgvector.Table) {
	t.Helper()
	db, err := sql.Open("pgx", liveDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tbl := pgvector.Table{Name: fmt.Sprintf("engi_vec_%d", time.Now().UnixNano())}
	quoted, _ := tbl.SQL()
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+quoted) })
	return db, tbl
}

func TestPgvectorLive(t *testing.T) {
	db, tbl := openLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	rows := []pgvector.Row{
		{ID: "x", Content: "along x", Metadata: map[string]any{"axis": "x", "n": 1}, Vector: []float32{1, 0, 0}},
		{ID: "y", Content: "along y", Metadata: map[string]any{"axis": "y"}, Vector: []float32{0, 1, 0}},
		{ID: "xy", Content: "diagonal", Metadata: map[string]any{"axis": "xy"}, Vector: []float32{1, 1, 0}},
	}
	opts := pgvector.InsertOptions{BatchSize: 2, Create: true, Index: true, Distance: pgvector.Cosine}
	if n, err := pgvector.Insert(ctx, db, tbl, rows, opts); err != nil || n != 3 {
		t.Fatalf("insert: %d %v", n, err)
	}
	// Upsert is idempotent and replaces content.
	rows[0].Content = "along x (v2)"
	if _, err := pgvector.Insert(ctx, db, tbl, rows[:1], pgvector.InsertOptions{BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	// For the query (1, 0.1, 0) the closest row is "x" by cosine and L2, but the
	// largest inner product is "xy" (1.1 against 1).
	tests := []struct {
		d       pgvector.Distance
		topID   string
		topText string
	}{
		{pgvector.Cosine, "x", "along x (v2)"},
		{pgvector.L2, "x", "along x (v2)"},
		{pgvector.InnerProduct, "xy", "diagonal"},
	}
	for _, tt := range tests {
		got, err := pgvector.Search(ctx, db, tbl, []float32{1, 0.1, 0}, 2, nil, tt.d)
		if err != nil || len(got) != 2 || got[0].ID != tt.topID {
			t.Fatalf("%s: %+v %v", tt.d, got, err)
		}
		if got[0].Content != tt.topText || got[0].Score < got[1].Score {
			t.Fatalf("%s: %+v", tt.d, got)
		}
	}
	got, err := pgvector.Search(ctx, db, tbl, []float32{1, 0, 0}, 5, map[string]any{"axis": "y"}, pgvector.Cosine)
	if err != nil || len(got) != 1 || got[0].ID != "y" {
		t.Fatalf("filter: %+v %v", got, err)
	}
	if _, err := pgvector.Search(ctx, db, tbl, []float32{1, 0}, 1, nil, pgvector.Cosine); err == nil {
		t.Fatal("wrong dimensions accepted")
	}
	if n, err := pgvector.Delete(ctx, db, tbl, []string{"x"}, nil); err != nil || n != 1 {
		t.Fatalf("delete ids: %d %v", n, err)
	}
	if n, err := pgvector.Delete(ctx, db, tbl, nil, map[string]any{"axis": "xy"}); err != nil || n != 1 {
		t.Fatalf("delete filter: %d %v", n, err)
	}
	if got, err := pgvector.Search(ctx, db, tbl, []float32{1, 0, 0}, 5, nil, pgvector.Cosine); err != nil || len(got) != 1 {
		t.Fatalf("remaining: %+v %v", got, err)
	}
}

// TestReplaceDocumentLive checks that re-inserting a document removes its old
// chunks in the same transaction: a shrunk document keeps no stale chunk, other
// documents and ids that only look like chunks stay, and a failed re-insert
// leaves the old chunks in place.
func TestReplaceDocumentLive(t *testing.T) {
	db, tbl := openLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	opts := pgvector.InsertOptions{BatchSize: 10, Create: true, Distance: pgvector.Cosine}
	chunk := func(id, doc string, v ...float32) pgvector.Row {
		return pgvector.Row{ID: id, Content: id, Metadata: map[string]any{"doc": doc}, Vector: v}
	}
	ids := func(t *testing.T, dims int) []string {
		t.Helper()
		query := make([]float32, dims)
		query[0] = 1
		got, err := pgvector.Search(ctx, db, tbl, query, 100, nil, pgvector.Cosine)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, m := range got {
			out = append(out, m.ID)
		}
		return out
	}
	same := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		seen := map[string]bool{}
		for _, id := range got {
			seen[id] = true
		}
		if len(got) != len(want) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
		for _, w := range want {
			if !seen[w] {
				t.Fatalf("ids = %v, want %v", got, want)
			}
		}
	}

	// Three chunks for "doc", one for "doc2", and "doc#x" which is not a chunk of "doc".
	first := []pgvector.Row{
		{ID: "doc#0", Document: "doc", Vector: []float32{1, 0, 0}},
		{ID: "doc#1", Document: "doc", Vector: []float32{0, 1, 0}},
		{ID: "doc#2", Document: "doc", Vector: []float32{0, 0, 1}},
		{ID: "doc2", Document: "doc2", Vector: []float32{1, 1, 0}},
		{ID: "doc#x", Vector: []float32{0, 1, 1}},
	}
	if _, err := pgvector.Insert(ctx, db, tbl, first, opts); err != nil {
		t.Fatal(err)
	}
	same(t, ids(t, 3), "doc#0", "doc#1", "doc#2", "doc2", "doc#x")

	// "doc" shrinks to one chunk; "doc2" grows to two chunks, in one call.
	shrink := []pgvector.Row{
		{ID: "doc", Document: "doc", Vector: []float32{1, 0, 0}},
		{ID: "doc2#0", Document: "doc2", Vector: []float32{1, 1, 0}},
		{ID: "doc2#1", Document: "doc2", Vector: []float32{0, 1, 1}},
	}
	if _, err := pgvector.Insert(ctx, db, tbl, shrink, opts); err != nil {
		t.Fatal(err)
	}
	same(t, ids(t, 3), "doc", "doc2#0", "doc2#1", "doc#x")

	// A failed re-insert (wrong dimensions) rolls the delete back: "doc" keeps its chunks.
	bad := []pgvector.Row{{ID: "doc", Document: "doc", Vector: []float32{1, 0}}}
	if _, err := pgvector.Insert(ctx, db, tbl, bad, pgvector.InsertOptions{BatchSize: 10}); err == nil {
		t.Fatal("wrong dimensions accepted")
	}
	same(t, ids(t, 3), "doc", "doc2#0", "doc2#1", "doc#x")

	// Rows without a document only upsert: nothing else is removed.
	if _, err := pgvector.Insert(ctx, db, tbl, []pgvector.Row{chunk("loose", "", 0, 0, 1)}, pgvector.InsertOptions{BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	same(t, ids(t, 3), "doc", "doc2#0", "doc2#1", "doc#x", "loose")
}
