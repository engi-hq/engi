package aitoolvectorsearch_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aitoolvectorsearch"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/node/pgvector/pgvectortest"
	"github.com/engi-hq/engi/internal/node/ragkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakePools struct{ db *sql.DB }

func (p fakePools) DB(context.Context, string, nodesdk.Credential) (*sql.DB, error) { return p.db, nil }

type fakeEmbedder struct {
	texts []string
	err   error
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) (port.EmbedResult, error) {
	f.texts = append(f.texts, texts...)
	if f.err != nil {
		return port.EmbedResult{}, f.err
	}
	return port.EmbedResult{Vectors: [][]float32{{1, 2}}, Dimensions: 2}, nil
}

var limits = ragkit.Limits{MaxTopK: 10, InsertBatchSize: 10, MaxDeleteIDs: 10}

func supply(t *testing.T, params map[string]any) (port.Tool, *pgvectortest.DB, *fakeEmbedder, error) {
	t.Helper()
	db, fake := pgvectortest.Open(t)
	n := aitoolvectorsearch.New(aitoolvectorsearch.Config{Pools: fakePools{db}, Limits: limits})
	in := nodetest.New(n.Describe(), nil, params)
	emb := &fakeEmbedder{}
	in.Subs = map[nodesdk.PortKind][]nodesdk.Supplied{nodesdk.PortAIEmbedding: {{Node: "Emb", Value: emb}}}
	in.Credentials = map[string]nodesdk.Credential{"engi.postgres": {Type: "engi.postgres", Data: map[string]any{}}}
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		return nil, fake, emb, err
	}
	tools, err := v.(port.ToolProvider).Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools %v %v", tools, err)
	}
	return tools[0], fake, emb, nil
}

func TestDescriptor(t *testing.T) {
	d := aitoolvectorsearch.New(aitoolvectorsearch.Config{}).Describe()
	if err := d.Validate(); err != nil || !d.IsSubNode() {
		t.Fatalf("err=%v subnode=%v", err, d.IsSubNode())
	}
	p, ok := d.PortOfKind(true, nodesdk.PortAIEmbedding, 0)
	if !ok || !p.Required || p.MaxConnections != 1 {
		t.Fatalf("port %+v", p)
	}
}

func TestToolDefinitionAndCall(t *testing.T) {
	tool, fake, emb, err := supply(t, map[string]any{"table": "docs", "name": "kb", "description": "Company handbook", "topK": float64(3), "filter": map[string]any{"src": "hb"}})
	if err != nil {
		t.Fatal(err)
	}
	def := tool.Definition()
	if def.Name != "kb" || def.Description != "Company handbook" {
		t.Fatalf("def %+v", def)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.Parameters, &schema); err != nil || schema["required"].([]any)[0] != "query" {
		t.Fatalf("schema %s %v", def.Parameters, err)
	}
	fake.Columns = []string{"id", "content", "metadata", "distance"}
	fake.Rows = [][]driver.Value{{"a", "Vacation is 25 days", `{"src":"hb"}`, 0.2}}
	res, err := tool.Call(context.Background(), json.RawMessage(`{"query":"how many vacation days?"}`))
	if err != nil || res.IsError {
		t.Fatalf("%+v %v", res, err)
	}
	if len(emb.texts) != 1 || emb.texts[0] != "how many vacation days?" {
		t.Fatalf("embedded %q", emb.texts)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil || len(out) != 1 || out[0]["text"] != "Vacation is 25 days" || out[0]["score"] != 0.8 || out[0]["id"] != "a" {
		t.Fatalf("content %s %v", res.Content, err)
	}
	c := fake.Calls[0]
	if !strings.Contains(c.SQL, `FROM "docs"`) || c.Args[1] != `{"src":"hb"}` || c.Args[2] != int64(3) {
		t.Fatalf("call %+v", c)
	}
}

func TestToolCallErrors(t *testing.T) {
	tool, fake, emb, err := supply(t, map[string]any{"table": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`not json`, `{}`, `{"query":"  "}`} {
		res, err := tool.Call(context.Background(), json.RawMessage(args))
		if err != nil || !res.IsError {
			t.Fatalf("%s: %+v %v", args, res, err)
		}
	}
	if len(emb.texts) != 0 || len(fake.Calls) != 0 {
		t.Fatal("work done for invalid arguments")
	}
	emb.err = errors.New("provider down")
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"query":"x"}`)); err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("err = %v", err)
	}
}

func TestSupplyValidation(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"bad name":     {"table": "docs", "name": "has space"},
		"bad table":    {"table": "a b"},
		"topK too big": {"table": "docs", "topK": float64(99)},
		"bad filter":   {"table": "docs", "filter": "[]"},
	} {
		if _, _, _, err := supply(t, params); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	n := aitoolvectorsearch.New(aitoolvectorsearch.Config{})
	if _, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
