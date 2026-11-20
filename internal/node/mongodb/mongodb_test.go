package mongodb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescriptorIsValid(t *testing.T) {
	d := New(nil, 10).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Type != "engi.db.mongodb" || d.Credentials[0].Type != "engi.mongodb" {
		t.Errorf("unexpected descriptor %+v", d)
	}
}

func TestExecuteWithoutClientsIsUnavailable(t *testing.T) {
	n := New(nil, 10)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"collection": "c"})
	if _, err := n.Execute(context.Background(), in); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func doParse(t *testing.T, params map[string]any, maxResults int) (request, error) {
	t.Helper()
	n := New(nil, maxResults)
	op, _ := params["operation"].(string)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"name": "from-item"}), params)
	return parse(context.Background(), in, op, 0, maxResults)
}

func TestParseFind(t *testing.T) {
	r, err := doParse(t, map[string]any{
		"operation": "find", "collection": "c", "filter": `{"age": {"$gt": 3}}`,
		"sort": `{"b": 1, "a": -1}`, "projection": map[string]any{"name": 1.0}, "limit": 5.0,
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if r.limit != 5 || len(r.filter) != 1 || r.sort[0].Key != "b" || r.sort[1].Key != "a" || len(r.projection) != 1 {
		t.Errorf("unexpected request %+v", r)
	}
	if r, _ = doParse(t, map[string]any{"operation": "find", "collection": "c", "limit": 500.0}, 100); r.limit != 100 {
		t.Errorf("limit must be capped at 100, got %d", r.limit)
	}
	if r, _ = doParse(t, map[string]any{"operation": "find", "collection": "c"}, 100); r.limit != 100 || len(r.filter) != 0 {
		t.Errorf("no limit means the cap, got %d", r.limit)
	}
	r, err = doParse(t, map[string]any{"operation": "find", "collection": "c", "filter": `{"_id": {"$oid": "65a1b2c3d4e5f60718293a4b"}}`}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.filter[0].Value.(bson.ObjectID); !ok {
		t.Errorf("extended JSON $oid must become an ObjectID, got %T", r.filter[0].Value)
	}
}

func TestParseWrites(t *testing.T) {
	r, err := doParse(t, map[string]any{"operation": "insert", "collection": "c"}, 10)
	if err != nil || len(r.documents) != 1 {
		t.Errorf("insert with no documents uses the item: %+v %v", r, err)
	}
	if r, err = doParse(t, map[string]any{"operation": "insert", "collection": "c", "documents": `[{"a":1},{"a":2}]`}, 10); err != nil || len(r.documents) != 2 {
		t.Errorf("insert array: %+v %v", r, err)
	}
	if r, err = doParse(t, map[string]any{"operation": "update", "collection": "c", "filter": `{"a":1}`, "update": `{"$set":{"b":2}}`, "multiple": true}, 10); err != nil || !r.multiple {
		t.Errorf("update: %+v %v", r, err)
	}
	if r, err = doParse(t, map[string]any{"operation": "update", "collection": "c", "filter": `{"a":1}`, "update": `[{"$set":{"b":2}}]`}, 10); err != nil {
		t.Errorf("pipeline update: %v", err)
	} else if _, ok := r.update.([]bson.D); !ok {
		t.Errorf("update pipeline type %T", r.update)
	}
	if r, err = doParse(t, map[string]any{"operation": "aggregate", "collection": "c", "pipeline": `[{"$match":{"a":1}},{"$sort":{"b":-1}}]`}, 7); err != nil || len(r.pipeline) != 2 || r.limit != 7 {
		t.Errorf("aggregate: %+v %v", r, err)
	}
}

func TestParseRejects(t *testing.T) {
	bad := []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"operation": "nope", "collection": "c"}, "unknown operation"},
		{map[string]any{"operation": "find", "collection": ""}, "collection"},
		{map[string]any{"operation": "delete", "collection": "c"}, "non-empty filter"},
		{map[string]any{"operation": "delete", "collection": "c", "filter": `{}`}, "non-empty filter"},
		{map[string]any{"operation": "update", "collection": "c", "update": `{"$set":{"a":1}}`}, "non-empty filter"},
		{map[string]any{"operation": "update", "collection": "c", "filter": `{"a":1}`}, "update must not be empty"},
		{map[string]any{"operation": "find", "collection": "c", "filter": `{bad`}, "filter"},
		{map[string]any{"operation": "find", "collection": "c", "limit": -1.0}, "limit"},
		{map[string]any{"operation": "aggregate", "collection": "c", "pipeline": `{"a":1}`}, "pipeline"},
		{map[string]any{"operation": "aggregate", "collection": "c", "pipeline": `[]`}, "at least one stage"},
		{map[string]any{"operation": "findOneAndUpdate", "collection": "c", "update": `{"$set":{"a":1}}`, "returnDocument": "later"}, "returnDocument"},
	}
	for _, b := range bad {
		if _, err := doParse(t, b.params, 10); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%v: want error containing %q, got %v", b.params, b.want, err)
		}
	}
}

type failingClients struct{ calls int }

func (f *failingClients) Mongo(context.Context, nodesdk.Credential) (*mongo.Client, string, error) {
	f.calls++
	return nil, "", errors.New("no mongo in unit tests")
}

func TestBadItemFailsBeforeConnecting(t *testing.T) {
	clients := &failingClients{}
	n := New(clients, 10)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"operation": "delete", "collection": "c"})
	in.Credentials = map[string]nodesdk.Credential{"engi.mongodb": {ID: "c"}}
	if _, err := n.Execute(context.Background(), in); err == nil || clients.calls != 0 {
		t.Fatalf("want a parse error without connecting, got %v (calls %d)", err, clients.calls)
	}
}

func TestPlainUsesExtendedJSON(t *testing.T) {
	id := bson.NewObjectID()
	m, err := plain(bson.D{{Key: "_id", Value: id}, {Key: "n", Value: int32(3)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := m["_id"].(map[string]any)["$oid"]; got != id.Hex() || m["n"] != 3.0 {
		t.Errorf("unexpected %v", m)
	}
}
