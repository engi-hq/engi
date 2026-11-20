//go:build integration

package mongodb_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"

	"github.com/engi-hq/engi/internal/adapter/mongopool"
	"github.com/engi-hq/engi/internal/node/mongodb"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestMongoDBIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := tcmongo.Run(ctx, "mongo:7")
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting mongodb container: %v", err)
	}
	uri, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool := mongopool.New(mongopool.Config{MaxPoolSize: 4, IdleTimeout: time.Minute, ConnectTimeout: 10 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = pool.Close() })
	n := mongodb.New(pool, 3)
	cred := nodesdk.Credential{Type: "engi.mongodb", ID: "cr", Version: 1, Data: map[string]any{"connectionString": uri, "database": "engi"}}

	run := func(t *testing.T, item map[string]any, params map[string]any) ([]nodesdk.Item, error) {
		t.Helper()
		params["collection"] = "people"
		in := nodetest.New(n.Describe(), nodetest.Items(item), params)
		in.Credentials = map[string]nodesdk.Credential{"engi.mongodb": cred}
		out, err := n.Execute(ctx, in)
		if err != nil {
			return nil, err
		}
		return out.Ports[0], nil
	}
	must := func(t *testing.T, params map[string]any) []nodesdk.Item {
		t.Helper()
		items, err := run(t, map[string]any{}, params)
		if err != nil {
			t.Fatalf("%v: %v", params, err)
		}
		return items
	}

	ins := must(t, map[string]any{"operation": "insert", "documents": `[{"name":"Ada","age":36},{"name":"Bob","age":25},{"name":"Cy","age":41},{"name":"Di","age":19}]`})
	if ins[0].JSON["insertedCount"] != 4.0 {
		t.Errorf("insert = %v", ins[0].JSON)
	}
	if one, err := run(t, map[string]any{"name": "Eve", "age": 50.0}, map[string]any{"operation": "insert"}); err != nil || one[0].JSON["insertedCount"] != 1.0 {
		t.Errorf("insert item json: %v %v", one, err)
	}

	found := must(t, map[string]any{"operation": "find", "filter": `{"age":{"$gte":25}}`, "sort": `{"age":-1}`, "projection": `{"_id":0,"name":1}`, "limit": 2.0})
	if len(found) != 2 || found[0].JSON["name"] != "Eve" || found[1].JSON["name"] != "Cy" || found[0].JSON["age"] != nil {
		t.Errorf("find = %v", found)
	}
	if all := must(t, map[string]any{"operation": "find"}); len(all) != 3 {
		t.Errorf("find without limit must be capped at 3, got %d", len(all))
	}
	id := must(t, map[string]any{"operation": "find", "filter": `{"name":"Ada"}`})[0].JSON["_id"].(map[string]any)["$oid"].(string)
	if byID := must(t, map[string]any{"operation": "find", "filter": `{"_id":{"$oid":"` + id + `"}}`}); len(byID) != 1 {
		t.Errorf("find by $oid = %v", byID)
	}

	up := must(t, map[string]any{"operation": "update", "filter": `{"name":"Bob"}`, "update": `{"$set":{"age":26}}`})
	if up[0].JSON["matchedCount"] != 1.0 || up[0].JSON["modifiedCount"] != 1.0 {
		t.Errorf("update = %v", up[0].JSON)
	}
	many := must(t, map[string]any{"operation": "update", "filter": `{"age":{"$gt":30}}`, "update": `{"$set":{"senior":true}}`, "multiple": true})
	if many[0].JSON["modifiedCount"] != 3.0 {
		t.Errorf("update many = %v", many[0].JSON)
	}
	fou := must(t, map[string]any{"operation": "findOneAndUpdate", "filter": `{"name":"Di"}`, "update": `{"$inc":{"age":1}}`})
	if len(fou) != 1 || fou[0].JSON["age"] != 20.0 {
		t.Errorf("findOneAndUpdate after = %v", fou)
	}
	if before := must(t, map[string]any{"operation": "findOneAndUpdate", "filter": `{"name":"Di"}`, "update": `{"$inc":{"age":1}}`, "returnDocument": "before"}); before[0].JSON["age"] != 20.0 {
		t.Errorf("findOneAndUpdate before = %v", before)
	}
	if none := must(t, map[string]any{"operation": "findOneAndUpdate", "filter": `{"name":"Nobody"}`, "update": `{"$set":{"a":1}}`}); len(none) != 0 {
		t.Errorf("no match must yield no item: %v", none)
	}

	agg := must(t, map[string]any{"operation": "aggregate", "pipeline": `[{"$group":{"_id":"$senior","count":{"$sum":1}}},{"$sort":{"count":-1}}]`})
	if len(agg) != 2 || agg[0].JSON["count"] != 3.0 {
		t.Errorf("aggregate = %v", agg)
	}

	if _, err := run(t, map[string]any{}, map[string]any{"operation": "delete", "filter": `{}`}); err == nil || !strings.Contains(err.Error(), "non-empty filter") {
		t.Errorf("empty-filter delete must be refused, got %v", err)
	}
	if del := must(t, map[string]any{"operation": "delete", "filter": `{"age":{"$lt":30}}`, "multiple": true}); del[0].JSON["deletedCount"] != 2.0 {
		t.Errorf("delete = %v", del[0].JSON)
	}
	if left := must(t, map[string]any{"operation": "find", "limit": 10.0}); len(left) != 3 {
		t.Errorf("after delete %d documents remain, want 3", len(left))
	}
}
