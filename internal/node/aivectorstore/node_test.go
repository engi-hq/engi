package aivectorstore_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aitextsplitter"
	"github.com/engi-hq/engi/internal/node/aivectorstore"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/node/pgvector/pgvectortest"
	"github.com/engi-hq/engi/internal/node/ragkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakePools struct {
	db  *sql.DB
	err error
	got nodesdk.Credential
	drv string
}

func (p *fakePools) DB(_ context.Context, drv string, cred nodesdk.Credential) (*sql.DB, error) {
	p.drv, p.got = drv, cred
	return p.db, p.err
}

// fakeEmbedder maps a text to [len(text), 1].
type fakeEmbedder struct {
	calls [][]string
	err   error
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) (port.EmbedResult, error) {
	f.calls = append(f.calls, texts)
	if f.err != nil {
		return port.EmbedResult{}, f.err
	}
	res := port.EmbedResult{Dimensions: 2}
	for _, t := range texts {
		res.Vectors = append(res.Vectors, []float32{float32(len([]rune(t))), 1})
	}
	return res, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { return len(p), nil }

var limits = ragkit.Limits{MaxTopK: 10, InsertBatchSize: 2, MaxDeleteIDs: 3}

type env struct {
	node  *aivectorstore.Node
	in    *nodetest.Input
	fake  *pgvectortest.DB
	emb   *fakeEmbedder
	pools *fakePools
}

func setup(t *testing.T, params map[string]any, items ...map[string]any) *env {
	t.Helper()
	db, fake := pgvectortest.Open(t)
	pools := &fakePools{db: db}
	n := aivectorstore.New(aivectorstore.Config{Pools: pools, Limits: limits, Rand: zeros{}})
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	in.Credentials = map[string]nodesdk.Credential{"engi.postgres": {Type: "engi.postgres", Data: map[string]any{"host": "h"}}}
	emb := &fakeEmbedder{}
	in.Subs = map[nodesdk.PortKind][]nodesdk.Supplied{nodesdk.PortAIEmbedding: {{Node: "Embeddings", Value: emb}}}
	return &env{node: n, in: in, fake: fake, emb: emb, pools: pools}
}

func (e *env) run() (nodesdk.Output, error) { return e.node.Execute(context.Background(), e.in) }

func TestDescriptor(t *testing.T) {
	d := aivectorstore.New(aivectorstore.Config{}).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	emb, ok := d.PortOfKind(true, nodesdk.PortAIEmbedding, 0)
	if !ok || !emb.Required || emb.MaxConnections != 1 {
		t.Fatalf("embedding port %+v", emb)
	}
	split, ok := d.PortOfKind(true, nodesdk.PortAITextSplitter, 0)
	if !ok || split.Required || split.MaxConnections != 1 {
		t.Fatalf("splitter port %+v", split)
	}
}

func TestInsertSplitsEmbedsAndUpserts(t *testing.T) {
	e := setup(t, map[string]any{"table": "docs", "schema": "rag", "createIfMissing": true, "createIndex": true,
		"metadata": map[string]any{"src": "a"}, "id": "doc1"},
		map[string]any{"text": "aaaa bbbb cccc"}, map[string]any{"text": "dd"})
	// Item-specific text via ParamFunc, like an expression would.
	texts := []string{"aaaa bbbb cccc", "dd"}
	e.in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "text" {
			return texts[i], true
		}
		if name == "id" {
			return []string{"doc1", "doc2"}[i], true
		}
		return nil, false
	}
	sp := aitextsplitter.New()
	sub := nodetest.New(sp.Describe(), nil, map[string]any{"chunkSize": float64(9), "chunkOverlap": float64(0), "separators": []any{" "}})
	var err error
	if e.in.Subs[nodesdk.PortAITextSplitter], err = nodetest.SupplyFrom(context.Background(), "Splitter", sp.(nodesdk.Supplier), sub); err != nil {
		t.Fatal(err)
	}
	out, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(e.emb.calls) != 1 || !reflect.DeepEqual(e.emb.calls[0], []string{"aaaa bbbb", "cccc", "dd"}) {
		t.Fatalf("embed calls %q", e.emb.calls)
	}
	got := nodetest.JSONs(out.Ports[0])
	if !reflect.DeepEqual(got[0]["ids"], []any{"doc1#0", "doc1#1"}) || got[0]["chunks"] != float64(2) || !reflect.DeepEqual(got[1]["ids"], []any{"doc2"}) {
		t.Fatalf("output %v", got)
	}
	if out.Ports[0][0].PairedItem[0].Item != 0 || out.Ports[0][1].PairedItem[0].Item != 1 {
		t.Fatalf("pairing %+v", out.Ports[0])
	}
	// Each document with an id first drops its earlier chunks, then the rows are upserted.
	sqls := e.fake.SQLs()
	if len(sqls) != 7 || !strings.HasPrefix(sqls[0], "CREATE EXTENSION") || !strings.Contains(sqls[1], `"rag"."docs"`) || !strings.Contains(sqls[1], "vector(2)") ||
		!strings.Contains(sqls[2], "hnsw") || !strings.HasPrefix(sqls[3], "DELETE") || !strings.HasPrefix(sqls[4], "DELETE") ||
		strings.Count(sqls[5], "($") != 2 || strings.Count(sqls[6], "($") != 1 {
		t.Fatalf("sql %v", sqls)
	}
	if e.fake.Calls[3].Args[0] != "doc1" || e.fake.Calls[4].Args[0] != "doc2" {
		t.Fatalf("delete args %v %v", e.fake.Calls[3].Args, e.fake.Calls[4].Args)
	}
	args := e.fake.Calls[5].Args
	if args[0] != "doc1#0" || args[1] != "aaaa bbbb" || args[2] != `{"chunkCount":2,"chunkIndex":0,"src":"a"}` || args[3] != "[9,1]" {
		t.Fatalf("args %v", args)
	}
	if e.pools.drv != "postgres" || e.pools.got.Data["host"] != "h" {
		t.Fatalf("pool %q %v", e.pools.drv, e.pools.got)
	}
}

func TestInsertWithoutSplitterGeneratesIDs(t *testing.T) {
	e := setup(t, map[string]any{"table": "docs", "text": "hello"}, map[string]any{"x": 1})
	out, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	ids := nodetest.JSONs(out.Ports[0])[0]["ids"].([]any)
	if len(ids) != 1 || ids[0] != "00000000-0000-4000-8000-000000000000" {
		t.Fatalf("ids %v", ids)
	}
	if sqls := e.fake.SQLs(); len(sqls) != 1 || !strings.HasPrefix(sqls[0], "INSERT") {
		t.Fatalf("sql %v (no DDL expected)", sqls)
	}
}

func TestInsertErrors(t *testing.T) {
	t.Run("empty text names the item", func(t *testing.T) {
		e := setup(t, map[string]any{"table": "docs", "text": ""}, map[string]any{})
		if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "text is empty") {
			t.Fatalf("err = %v", err)
		}
		if len(e.emb.calls) != 0 {
			t.Fatal("embedded despite error")
		}
	})
	t.Run("embedder failure", func(t *testing.T) {
		e := setup(t, map[string]any{"table": "docs", "text": "x"}, map[string]any{})
		e.emb.err = errors.New("rate limited")
		if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("err = %v", err)
		}
		if len(e.fake.Calls) != 0 {
			t.Fatal("database touched")
		}
	})
	t.Run("bad table", func(t *testing.T) {
		e := setup(t, map[string]any{"table": "docs; drop table x", "text": "x"}, map[string]any{})
		if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "identifier") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no embedder", func(t *testing.T) {
		e := setup(t, map[string]any{"table": "docs", "text": "x"}, map[string]any{})
		e.in.Subs = nil
		if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "embedding") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("pools unavailable", func(t *testing.T) {
		n := aivectorstore.New(aivectorstore.Config{Limits: limits})
		in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"table": "docs", "text": "x"})
		in.Subs = map[nodesdk.PortKind][]nodesdk.Supplied{nodesdk.PortAIEmbedding: {{Value: &fakeEmbedder{}}}}
		if _, err := n.Execute(context.Background(), in); !errors.Is(err, nodesdk.ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("database error", func(t *testing.T) {
		e := setup(t, map[string]any{"table": "docs", "text": "x"}, map[string]any{})
		e.fake.FailOn, e.fake.FailErr = "INSERT", errors.New("expected 3 dimensions, not 2")
		if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "different embedding size") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSearch(t *testing.T) {
	e := setup(t, map[string]any{"operation": "search", "table": "docs", "query": "hello", "topK": float64(2), "filter": `{"src":"a"}`, "distance": "l2"},
		map[string]any{}, map[string]any{})
	e.fake.Columns = []string{"id", "content", "metadata", "distance"}
	e.fake.Rows = [][]driver.Value{{"a", "alpha", `{"src":"a"}`, 1.0}}
	out, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(e.emb.calls) != 1 || len(e.emb.calls[0]) != 2 {
		t.Fatalf("queries should be embedded in one call: %q", e.emb.calls)
	}
	got := nodetest.JSONs(out.Ports[0])
	if len(got) != 2 || got[0]["id"] != "a" || got[0]["text"] != "alpha" || got[0]["score"] != 0.5 || got[0]["distance"] != 1.0 ||
		got[0]["metadata"].(map[string]any)["src"] != "a" {
		t.Fatalf("output %v", got)
	}
	if out.Ports[0][1].PairedItem[0].Item != 1 {
		t.Fatalf("pairing %+v", out.Ports[0][1])
	}
	c := e.fake.Calls[0]
	if !strings.Contains(c.SQL, "<->") || !strings.Contains(c.SQL, "metadata @>") || c.Args[1] != `{"src":"a"}` || c.Args[2] != int64(2) {
		t.Fatalf("call %+v", c)
	}
}

func TestSearchValidation(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"topK too big": {"query": "q", "topK": float64(11)},
		"topK zero":    {"query": "q", "topK": float64(0)},
		"empty query":  {"query": ""},
		"bad filter":   {"query": "q", "filter": "[1]"},
		"bad distance": {"query": "q", "distance": "dot"},
	} {
		t.Run(name, func(t *testing.T) {
			params["operation"], params["table"] = "search", "docs"
			e := setup(t, params, map[string]any{})
			if _, err := e.run(); err == nil {
				t.Fatal("expected an error")
			}
			if len(e.fake.Calls) != 0 {
				t.Fatal("database touched")
			}
		})
	}
}

func TestDelete(t *testing.T) {
	e := setup(t, map[string]any{"operation": "delete", "table": "docs", "ids": "a, b", "filter": map[string]any{"k": "v"}}, map[string]any{})
	e.fake.Affected = 2
	out, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	if got := nodetest.JSONs(out.Ports[0])[0]["deleted"]; got != float64(2) {
		t.Fatalf("deleted %v", got)
	}
	if len(e.emb.calls) != 0 {
		t.Fatal("delete should not embed")
	}
	if c := e.fake.Calls[0]; !strings.HasPrefix(c.SQL, `DELETE FROM "docs" WHERE id IN ($1, $2) AND metadata @>`) {
		t.Fatalf("sql %q", c.SQL)
	}
}

func TestDeleteGuards(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"no criteria": {},
		"too many":    {"ids": "a,b,c,d"},
	} {
		t.Run(name, func(t *testing.T) {
			params["operation"], params["table"] = "delete", "docs"
			e := setup(t, params, map[string]any{})
			if _, err := e.run(); err == nil {
				t.Fatal("expected an error")
			}
			if len(e.fake.Calls) != 0 {
				t.Fatalf("database touched: %v", e.fake.SQLs())
			}
		})
	}
}

func TestNoItemsAndUnknownOperation(t *testing.T) {
	e := setup(t, map[string]any{"table": "docs"})
	out, err := e.run()
	if err != nil || len(out.Ports) != 1 || len(out.Ports[0]) != 0 {
		t.Fatalf("%v %v", out, err)
	}
	e = setup(t, map[string]any{"table": "docs", "operation": "drop"}, map[string]any{})
	if _, err := e.run(); err == nil || !strings.Contains(err.Error(), "unknown operation") {
		t.Fatalf("err = %v", err)
	}
}
