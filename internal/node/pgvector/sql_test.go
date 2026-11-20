package pgvector_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/pgvector"
	"github.com/engi-hq/engi/internal/node/pgvector/pgvectortest"
)

var tbl = pgvector.Table{Schema: "rag", Name: "docs"}

func TestTableValidation(t *testing.T) {
	for name, tt := range map[string]struct {
		tbl pgvector.Table
		ok  bool
		sql string
	}{
		"plain":           {pgvector.Table{Name: "docs"}, true, `"docs"`},
		"schema":          {tbl, true, `"rag"."docs"`},
		"underscore":      {pgvector.Table{Name: "_d1"}, true, `"_d1"`},
		"empty":           {pgvector.Table{}, false, ""},
		"space":           {pgvector.Table{Name: "my docs"}, false, ""},
		"quote":           {pgvector.Table{Name: `a"b`}, false, ""},
		"semicolon":       {pgvector.Table{Name: "a;drop table x"}, false, ""},
		"leading digit":   {pgvector.Table{Name: "1a"}, false, ""},
		"too long":        {pgvector.Table{Name: strings.Repeat("a", pgvector.MaxIdentLen+1)}, false, ""},
		"bad schema":      {pgvector.Table{Schema: "s-1", Name: "t"}, false, ""},
		"unicode letters": {pgvector.Table{Name: "tä"}, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tt.tbl.SQL()
			if tt.ok != (err == nil) || got != tt.sql {
				t.Fatalf("got %q, %v", got, err)
			}
			if !tt.ok && !errors.Is(err, pgvector.ErrInvalidIdentifier) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLiteral(t *testing.T) {
	got, err := pgvector.Literal([]float32{1, -0.5, 0.1, 1e-7})
	if err != nil || got != "[1,-0.5,0.1,1e-07]" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range [][]float32{nil, {float32(math.NaN())}, {1, float32(math.Inf(1))}} {
		if _, err := pgvector.Literal(bad); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

func TestDDL(t *testing.T) {
	got, err := pgvector.CreateTableSQL(tbl, 3)
	want := `CREATE TABLE IF NOT EXISTS "rag"."docs" (id text PRIMARY KEY, content text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}'::jsonb, embedding vector(3) NOT NULL)`
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := pgvector.CreateTableSQL(tbl, 0); err == nil {
		t.Fatal("dims 0 accepted")
	}
	for d, op := range map[pgvector.Distance]string{pgvector.Cosine: "vector_cosine_ops", pgvector.L2: "vector_l2_ops", pgvector.InnerProduct: "vector_ip_ops"} {
		idx, err := pgvector.CreateIndexSQL(tbl, d)
		if err != nil || !strings.Contains(idx, "USING hnsw (embedding "+op+")") || !strings.Contains(idx, `"docs_embedding_idx"`) {
			t.Fatalf("%s: %q, %v", d, idx, err)
		}
	}
	if _, err := pgvector.CreateIndexSQL(tbl, "dot"); err == nil {
		t.Fatal("unknown distance accepted")
	}
}

func TestUpsertStatements(t *testing.T) {
	rows := []pgvector.Row{
		{ID: "a", Content: "alpha", Metadata: map[string]any{"k": "v"}, Vector: []float32{1, 2}},
		{ID: "b", Content: "it's", Vector: []float32{3, 4}},
		{ID: "c", Content: "c", Vector: []float32{5, 6}},
	}
	st, err := pgvector.UpsertStatements(tbl, rows, 2)
	if err != nil || len(st) != 2 {
		t.Fatalf("%v %v", st, err)
	}
	wantSQL := `INSERT INTO "rag"."docs" (id, content, metadata, embedding) VALUES ($1, $2, $3::text::jsonb, $4::text::vector), ($5, $6, $7::text::jsonb, $8::text::vector) ON CONFLICT (id) DO UPDATE SET content = EXCLUDED.content, metadata = EXCLUDED.metadata, embedding = EXCLUDED.embedding`
	if st[0].SQL != wantSQL {
		t.Fatalf("sql %q", st[0].SQL)
	}
	if !reflect.DeepEqual(st[0].Args, []any{"a", "alpha", `{"k":"v"}`, "[1,2]", "b", "it's", `{}`, "[3,4]"}) {
		t.Fatalf("args %#v", st[0].Args)
	}
	if strings.Contains(st[0].SQL, "alpha") || strings.Contains(st[0].SQL, "it's") {
		t.Fatal("user data in SQL text")
	}
	if len(st[1].Args) != 4 {
		t.Fatalf("second batch %#v", st[1].Args)
	}
}

func TestUpsertStatementsRejects(t *testing.T) {
	for name, rows := range map[string][]pgvector.Row{
		"no id":      {{Vector: []float32{1}}},
		"mixed dims": {{ID: "a", Vector: []float32{1}}, {ID: "b", Vector: []float32{1, 2}}},
		"empty vec":  {{ID: "a"}},
		"nan":        {{ID: "a", Vector: []float32{float32(math.NaN())}}},
		"bad meta":   {{ID: "a", Vector: []float32{1}, Metadata: map[string]any{"f": func() {}}}},
	} {
		if _, err := pgvector.UpsertStatements(tbl, rows, 10); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := pgvector.UpsertStatements(tbl, []pgvector.Row{{ID: "a", Vector: []float32{1}}}, 0); err == nil {
		t.Error("batch 0 accepted")
	}
	if _, err := pgvector.UpsertStatements(pgvector.Table{Name: "x y"}, nil, 1); err == nil {
		t.Error("bad table accepted")
	}
}

func TestSearchStatement(t *testing.T) {
	st, err := pgvector.SearchStatement(tbl, []float32{1, 2}, 5, nil, pgvector.Cosine)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT id, content, metadata::text, embedding <=> $1::text::vector AS distance FROM "rag"."docs" ORDER BY embedding <=> $1::text::vector LIMIT $2`
	if st.SQL != want || !reflect.DeepEqual(st.Args, []any{"[1,2]", 5}) {
		t.Fatalf("%q %#v", st.SQL, st.Args)
	}
	st, err = pgvector.SearchStatement(tbl, []float32{1}, 3, map[string]any{"src": "a"}, pgvector.L2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `embedding <-> $1::text::vector AS distance FROM "rag"."docs" WHERE metadata @> $2::text::jsonb ORDER BY`) ||
		!strings.HasSuffix(st.SQL, "LIMIT $3") || !reflect.DeepEqual(st.Args, []any{"[1]", `{"src":"a"}`, 3}) {
		t.Fatalf("%q %#v", st.SQL, st.Args)
	}
	st, _ = pgvector.SearchStatement(tbl, []float32{1}, 1, nil, pgvector.InnerProduct)
	if !strings.Contains(st.SQL, "<#>") {
		t.Fatal(st.SQL)
	}
	if _, err := pgvector.SearchStatement(tbl, []float32{1}, 0, nil, pgvector.Cosine); err == nil {
		t.Error("topK 0 accepted")
	}
	if _, err := pgvector.SearchStatement(tbl, []float32{1}, 1, nil, "x"); err == nil {
		t.Error("unknown distance accepted")
	}
}

func TestDeleteStatement(t *testing.T) {
	st, err := pgvector.DeleteStatement(tbl, []string{"a", "b"}, nil)
	if err != nil || st.SQL != `DELETE FROM "rag"."docs" WHERE id IN ($1, $2)` || !reflect.DeepEqual(st.Args, []any{"a", "b"}) {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = pgvector.DeleteStatement(tbl, []string{"a"}, map[string]any{"k": 1})
	if err != nil || st.SQL != `DELETE FROM "rag"."docs" WHERE id IN ($1) AND metadata @> $2::text::jsonb` || st.Args[1] != `{"k":1}` {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := pgvector.DeleteStatement(tbl, nil, nil); err == nil {
		t.Fatal("unconditional delete accepted")
	}
}

func TestScore(t *testing.T) {
	if got := pgvector.Cosine.ScoreOf(0.25); got != 0.75 {
		t.Fatal(got)
	}
	if got := pgvector.L2.ScoreOf(1); got != 0.5 {
		t.Fatal(got)
	}
	if got := pgvector.InnerProduct.ScoreOf(-0.8); got != 0.8 {
		t.Fatal(got)
	}
}

func TestInsertRunsSetupAndBatchesInOneTransaction(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	rows := []pgvector.Row{{ID: "a", Vector: []float32{1, 2}}, {ID: "b", Vector: []float32{3, 4}}, {ID: "c", Vector: []float32{5, 6}}}
	n, err := pgvector.Insert(context.Background(), db, tbl, rows, pgvector.InsertOptions{BatchSize: 2, Create: true, Index: true, Distance: pgvector.Cosine})
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	sqls := fake.SQLs()
	if len(sqls) != 5 || sqls[0] != pgvector.CreateExtensionSQL || !strings.Contains(sqls[1], "vector(2)") || !strings.Contains(sqls[2], "hnsw") ||
		!strings.HasPrefix(sqls[3], "INSERT") || !strings.HasPrefix(sqls[4], "INSERT") {
		t.Fatalf("calls %v", sqls)
	}
	if fake.Begins != 1 || fake.Commits != 1 || fake.Rollbacks != 0 {
		t.Fatalf("tx %d/%d/%d", fake.Begins, fake.Commits, fake.Rollbacks)
	}
}

func TestInsertWithoutCreateSkipsDDLAndRollsBackOnError(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	fake.FailOn = "INSERT"
	fake.FailErr = errors.New("expected 3 dimensions, not 2")
	_, err := pgvector.Insert(context.Background(), db, tbl, []pgvector.Row{{ID: "a", Vector: []float32{1, 2}}}, pgvector.InsertOptions{BatchSize: 10})
	if err == nil || !strings.Contains(err.Error(), "different embedding size") {
		t.Fatalf("err = %v", err)
	}
	if got := fake.SQLs(); len(got) != 1 || !strings.HasPrefix(got[0], "INSERT") {
		t.Fatalf("calls %v", got)
	}
	if fake.Commits != 0 || fake.Rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", fake.Commits, fake.Rollbacks)
	}
}

func TestInsertNothing(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	n, err := pgvector.Insert(context.Background(), db, tbl, nil, pgvector.InsertOptions{BatchSize: 1, Create: true})
	if err != nil || n != 0 || len(fake.Calls) != 0 {
		t.Fatalf("%d %v %v", n, err, fake.Calls)
	}
}

func TestSearchScansRows(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	fake.Columns = []string{"id", "content", "metadata", "distance"}
	fake.Rows = [][]driver.Value{{"a", "alpha", `{"src":"x"}`, 0.1}, {"b", "beta", `{}`, 0.4}}
	got, err := pgvector.Search(context.Background(), db, tbl, []float32{1, 0}, 2, nil, pgvector.Cosine)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].ID != "a" || got[0].Metadata["src"] != "x" || got[0].Distance != 0.1 || got[0].Score != 0.9 {
		t.Fatalf("%+v", got[0])
	}
	fake.Rows = [][]driver.Value{{"a", "alpha", `not json`, 0.1}}
	if _, err := pgvector.Search(context.Background(), db, tbl, []float32{1}, 1, nil, pgvector.Cosine); err == nil {
		t.Fatal("bad metadata accepted")
	}
}

func TestDeleteCountsRows(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	fake.Affected = 2
	n, err := pgvector.Delete(context.Background(), db, tbl, []string{"a", "b"}, nil)
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestDocumentDeleteStatement(t *testing.T) {
	st, err := pgvector.DocumentDeleteStatement(tbl, "doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.SQL, `DELETE FROM "`) || !strings.Contains(st.SQL, "id = $1::text") ||
		!strings.Contains(st.SQL, "left(id, length($2::text)) = $2::text") || strings.Contains(st.SQL, "doc-1") {
		t.Fatalf("sql = %s", st.SQL)
	}
	if len(st.Args) != 2 || st.Args[0] != "doc-1" || st.Args[1] != "doc-1#" {
		t.Fatalf("args = %v", st.Args)
	}
	if _, err := pgvector.DocumentDeleteStatement(tbl, ""); err == nil {
		t.Fatal("empty document id accepted")
	}
	if _, err := pgvector.DocumentDeleteStatement(pgvector.Table{Name: "bad name;"}, "doc"); err == nil {
		t.Fatal("invalid table accepted")
	}
}

func TestInsertReplacesEachDocumentBeforeInserting(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	rows := []pgvector.Row{
		{ID: "d", Document: "d", Vector: []float32{1, 2}},
		{ID: "d#1", Document: "d", Vector: []float32{3, 4}},
		{ID: "e", Document: "e", Vector: []float32{5, 6}},
		{ID: "random", Vector: []float32{7, 8}},
	}
	if _, err := pgvector.Insert(context.Background(), db, tbl, rows, pgvector.InsertOptions{BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	sqls := fake.SQLs()
	if len(sqls) != 3 || !strings.HasPrefix(sqls[0], "DELETE") || !strings.HasPrefix(sqls[1], "DELETE") || !strings.HasPrefix(sqls[2], "INSERT") {
		t.Fatalf("calls %v", sqls)
	}
	if got := fake.Calls[0].Args; len(got) != 2 || got[0] != "d" || got[1] != "d#" {
		t.Fatalf("first delete args %v", got)
	}
	if fake.Begins != 1 || fake.Commits != 1 {
		t.Fatalf("tx %d/%d", fake.Begins, fake.Commits)
	}
}

func TestInsertWithoutDocumentOnlyUpserts(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	rows := []pgvector.Row{{ID: "a", Vector: []float32{1}}, {ID: "b", Vector: []float32{2}}}
	if _, err := pgvector.Insert(context.Background(), db, tbl, rows, pgvector.InsertOptions{BatchSize: 10}); err != nil {
		t.Fatal(err)
	}
	if sqls := fake.SQLs(); len(sqls) != 1 || !strings.HasPrefix(sqls[0], "INSERT") {
		t.Fatalf("calls %v", sqls)
	}
}

func TestInsertRollsBackWhenReplacingFails(t *testing.T) {
	db, fake := pgvectortest.Open(t)
	fake.FailOn = "DELETE"
	fake.FailErr = errors.New("permission denied")
	_, err := pgvector.Insert(context.Background(), db, tbl, []pgvector.Row{{ID: "d", Document: "d", Vector: []float32{1}}}, pgvector.InsertOptions{BatchSize: 10})
	if err == nil || !strings.Contains(err.Error(), `chunks of document "d"`) {
		t.Fatalf("err = %v", err)
	}
	if got := fake.SQLs(); len(got) != 1 || !strings.HasPrefix(got[0], "DELETE") {
		t.Fatalf("calls %v", got)
	}
	if fake.Commits != 0 || fake.Rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", fake.Commits, fake.Rollbacks)
	}
}
