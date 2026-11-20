package sqlnode_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/node/postgres"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescriptorIsValid(t *testing.T) {
	d := postgres.New(nil).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Type != "engi.db.postgres" || len(d.Credentials) != 1 || d.Credentials[0].Type != "engi.postgres" {
		t.Errorf("unexpected descriptor %+v", d)
	}
}

func TestExecuteWithoutPoolsIsUnavailable(t *testing.T) {
	n := postgres.New(nil)
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}), map[string]any{"query": "select 1"})
	_, err := n.Execute(context.Background(), in)
	if !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

type countingPools struct{ calls int }

func (c *countingPools) DB(context.Context, string, nodesdk.Credential) (*sql.DB, error) {
	c.calls++
	return nil, errors.New("no database in unit tests")
}

func TestBadStatementsFailBeforeConnecting(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"delete without conditions", map[string]any{"operation": "delete", "table": "t"}, "delete needs at least one condition"},
		{"unknown operator", map[string]any{"operation": "select", "table": "t",
			"where": []any{map[string]any{"column": "a", "operator": "nope", "value": 1.0}}}, "unknown operator"},
		{"update without match", map[string]any{"operation": "update", "table": "t"}, "match column"},
		{"bad transaction", map[string]any{"operation": "select", "table": "t", "transaction": "weird"}, "transaction mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pools := &countingPools{}
			n := postgres.New(pools)
			in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}), tt.params)
			in.Credentials = map[string]nodesdk.Credential{"engi.postgres": {Type: "engi.postgres", ID: "c"}}
			_, err := n.Execute(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
			if pools.calls != 0 {
				t.Error("pool must not be used when the statement is invalid")
			}
		})
	}
}

func TestEmptyInputDoesNothing(t *testing.T) {
	n := postgres.New(&countingPools{})
	out, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nil, nil))
	if err != nil || len(out.Ports) != 1 || len(out.Ports[0]) != 0 {
		t.Fatalf("got %+v, %v", out, err)
	}
}
