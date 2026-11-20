package redis

import (
	"context"
	"errors"
	"strings"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescriptorIsValid(t *testing.T) {
	d := New(nil, 10).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Type != "engi.db.redis" || d.Credentials[0].Type != "engi.redis" {
		t.Errorf("unexpected descriptor %+v", d)
	}
}

func TestExecuteWithoutClientsIsUnavailable(t *testing.T) {
	n := New(nil, 10)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}), map[string]any{"key": "k"})
	if _, err := n.Execute(context.Background(), in); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestParse(t *testing.T) {
	n := New(nil, 10)
	do := func(params map[string]any) (request, error) {
		op, _ := params["operation"].(string)
		return parse(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params), op, 0)
	}
	r, err := do(map[string]any{"operation": "set", "key": "k", "value": map[string]any{"a": 1.0}, "ttl": 1.5})
	if err != nil || r.value != `{"a":1}` || r.ttl.Milliseconds() != 1500 {
		t.Errorf("set: %+v %v", r, err)
	}
	if r, err = do(map[string]any{"operation": "decr", "key": "k", "amount": 3.0}); err != nil || r.amount != 3 {
		t.Errorf("decr: %+v %v", r, err)
	}
	if r, err = do(map[string]any{"operation": "listPop", "key": "k", "side": "right"}); err != nil || !r.right {
		t.Errorf("listPop: %+v %v", r, err)
	}
	if r, err = do(map[string]any{"operation": "keys", "pattern": ""}); err != nil || r.pattern != "*" {
		t.Errorf("keys: %+v %v", r, err)
	}
	bad := []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{"operation": "nope", "key": "k"}, "unknown operation"},
		{map[string]any{"operation": "get", "key": ""}, "key must not be empty"},
		{map[string]any{"operation": "set", "key": "k", "ttl": -1.0}, "ttl"},
		{map[string]any{"operation": "incr", "key": "k", "amount": 1.5}, "whole number"},
		{map[string]any{"operation": "hashSet", "key": "k", "fields": map[string]any{}}, "at least one field"},
		{map[string]any{"operation": "listPush", "key": "k", "side": "middle"}, "list end"},
		{map[string]any{"operation": "publish", "channel": ""}, "channel"},
	}
	for _, b := range bad {
		if _, err := do(b.params); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%v: want error containing %q, got %v", b.params, b.want, err)
		}
	}
}

type failingClients struct{ calls int }

func (f *failingClients) Redis(context.Context, nodesdk.Credential) (*goredis.Client, error) {
	f.calls++
	return nil, errors.New("no redis in unit tests")
}

func TestBadItemFailsBeforeConnecting(t *testing.T) {
	clients := &failingClients{}
	n := New(clients, 10)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"operation": "get", "key": ""})
	in.Credentials = map[string]nodesdk.Credential{"engi.redis": {ID: "c"}}
	if _, err := n.Execute(context.Background(), in); err == nil || clients.calls != 0 {
		t.Fatalf("want a parse error without connecting, got %v (calls %d)", err, clients.calls)
	}
}
