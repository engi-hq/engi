//go:build integration

// Package sqlsuite is the integration suite shared by the Postgres and MySQL
// nodes. Each node's integration test starts a real database in a container
// and hands the suite a pool manager and a credential for it.
package sqlsuite

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/dbpool"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/node/sqlnode"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Env describes the database under test.
type Env struct {
	// Driver is port.DriverPostgres or port.DriverMySQL.
	Driver string
	// CredType is the credential type name (also the binding name).
	CredType string
	// Cred holds the connection data of the running container.
	Cred map[string]any
	// New builds the node under test.
	New func(sqlnode.Pools) nodesdk.Node
	// Placeholders returns the n-th query placeholder ($n or ?).
	Placeholder func(n int) string
	// Returning is true when writes return the affected rows (Postgres).
	Returning bool
	// Timestamp is the text written into the timestamp column (2024-05-06 07:08:09 UTC in the dialect's accepted syntax).
	Timestamp string
	// DDL creates the test tables; Types is the DDL of the column-type table.
	DDL []string
}

type harness struct {
	t     *testing.T
	env   Env
	pools *dbpool.Manager
	ver   int64
}

func (h *harness) cred() nodesdk.Credential {
	return nodesdk.Credential{Type: h.env.CredType, ID: "cr_itest", Version: h.ver, Data: h.env.Cred}
}

// run executes the node with the given parameters over the items.
func (h *harness) run(items []map[string]any, params map[string]any) ([]map[string]any, error) {
	h.t.Helper()
	n := h.env.New(h.pools)
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	in.Credentials = map[string]nodesdk.Credential{h.env.CredType: h.cred()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := n.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	return nodetest.JSONs(out.Ports[0]), nil
}

func (h *harness) must(items []map[string]any, params map[string]any) []map[string]any {
	h.t.Helper()
	out, err := h.run(items, params)
	if err != nil {
		h.t.Fatalf("node failed: %v", err)
	}
	return out
}

func (h *harness) reset() {
	h.t.Helper()
	h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery", "query": "DELETE FROM engi_people"})
}

func (h *harness) names(order string) []string {
	h.t.Helper()
	rows := h.must([]map[string]any{{}}, map[string]any{"operation": "select", "table": "engi_people", "sortColumn": order})
	var out []string
	for _, r := range rows {
		out = append(out, r["name"].(string))
	}
	return out
}

func equal(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

// Run executes the whole suite.
func Run(t *testing.T, env Env) {
	pools := dbpool.New(dbpool.Config{MaxOpenConns: 5, MaxIdleConns: 2, IdleTimeout: time.Minute, MaxLifetime: time.Hour, ConnectTimeout: 10 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = pools.Close() })
	h := &harness{t: t, env: env, pools: pools, ver: 1}
	for _, ddl := range env.DDL {
		h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery", "query": ddl})
	}
	people := func(rows ...map[string]any) []map[string]any { return rows }
	seed := func() {
		h.reset()
		h.must(people(
			map[string]any{"id": 1.0, "name": "alice", "age": 30.0},
			map[string]any{"id": 2.0, "name": "bob", "age": 25.0},
			map[string]any{"id": 3.0, "name": "carol", "age": 41.0},
		), map[string]any{"operation": "insert", "table": "engi_people"})
	}

	t.Run("executeQuery binds parameters", func(t *testing.T) {
		h.t = t
		h.reset()
		evil := "x'); DROP TABLE engi_people; --"
		h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery",
			"query":       "INSERT INTO engi_people (id, name, age) VALUES (" + env.Placeholder(1) + ", " + env.Placeholder(2) + ", " + env.Placeholder(3) + ")",
			"queryParams": []any{7.0, evil, 9.0}})
		rows := h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery",
			"query": "SELECT id, name, age FROM engi_people WHERE name = " + env.Placeholder(1), "queryParams": []any{evil}})
		if len(rows) != 1 || rows[0]["name"] != evil || rows[0]["id"] != int64(7) {
			t.Fatalf("got %#v", rows)
		}
	})

	t.Run("executeQuery once versus per item", func(t *testing.T) {
		h.t = t
		items := []map[string]any{{"a": 1.0}, {"a": 2.0}, {"a": 3.0}}
		perItem := h.must(items, map[string]any{"operation": "executeQuery", "query": "SELECT 1 AS one"})
		once := h.must(items, map[string]any{"operation": "executeQuery", "query": "SELECT 1 AS one", "runMode": "once"})
		if len(perItem) != 3 || len(once) != 1 {
			t.Fatalf("per item %d rows, once %d rows", len(perItem), len(once))
		}
	})

	t.Run("select where sort limit columns", func(t *testing.T) {
		h.t = t
		seed()
		rows := h.must([]map[string]any{{}}, map[string]any{"operation": "select", "table": "engi_people", "columns": "name,age",
			"where":      []any{map[string]any{"column": "age", "operator": "greaterThan", "value": 20.0}, map[string]any{"column": "name", "operator": "notEqual", "value": "carol"}},
			"sortColumn": "age", "sortDirection": "desc", "limit": 1.0})
		if len(rows) != 1 || rows[0]["name"] != "alice" || len(rows[0]) != 2 {
			t.Fatalf("got %#v", rows)
		}
		in := h.must([]map[string]any{{}}, map[string]any{"operation": "select", "table": "engi_people", "sortColumn": "id",
			"where": []any{map[string]any{"column": "id", "operator": "in", "value": "1,3"}}})
		if len(in) != 2 || in[1]["name"] != "carol" {
			t.Fatalf("in: %#v", in)
		}
		or := h.must([]map[string]any{{}}, map[string]any{"operation": "select", "table": "engi_people", "combinator": "or", "sortColumn": "id",
			"where": []any{map[string]any{"column": "name", "operator": "equal", "value": "bob"}, map[string]any{"column": "age", "operator": "greaterThan", "value": 40.0}}})
		if len(or) != 2 {
			t.Fatalf("or: %#v", or)
		}
	})

	t.Run("insert define mode and returning", func(t *testing.T) {
		h.t = t
		h.reset()
		out := h.must([]map[string]any{{}}, map[string]any{"operation": "insert", "table": "engi_people", "dataMode": "define",
			"values": []any{map[string]any{"name": "id", "value": 5.0}, map[string]any{"name": "name", "value": "dave"}}})
		if env.Returning {
			if len(out) != 1 || out[0]["name"] != "dave" {
				t.Fatalf("returning: %#v", out)
			}
		} else if len(out) != 1 || out[0]["affectedRows"] != int64(1) {
			t.Fatalf("affected: %#v", out)
		}
	})

	t.Run("update by match columns", func(t *testing.T) {
		h.t = t
		seed()
		h.must(people(map[string]any{"id": 2.0, "name": "robert", "age": 26.0}),
			map[string]any{"operation": "update", "table": "engi_people", "matchColumns": "id"})
		rows := h.must([]map[string]any{{}}, map[string]any{"operation": "select", "table": "engi_people",
			"where": []any{map[string]any{"column": "id", "value": 2.0}}})
		if rows[0]["name"] != "robert" || rows[0]["age"] != int64(26) {
			t.Fatalf("got %#v", rows)
		}
		if !equal(h.names("id"), []string{"alice", "robert", "carol"}) {
			t.Fatal("other rows changed")
		}
	})

	t.Run("upsert inserts then updates", func(t *testing.T) {
		h.t = t
		seed()
		h.must(people(map[string]any{"id": 3.0, "name": "caroline", "age": 42.0}, map[string]any{"id": 4.0, "name": "dan", "age": 19.0}),
			map[string]any{"operation": "upsert", "table": "engi_people", "matchColumns": "id"})
		if !equal(h.names("id"), []string{"alice", "bob", "caroline", "dan"}) {
			t.Fatalf("got %v", h.names("id"))
		}
	})

	t.Run("delete", func(t *testing.T) {
		h.t = t
		seed()
		out := h.must([]map[string]any{{}}, map[string]any{"operation": "delete", "table": "engi_people",
			"where": []any{map[string]any{"column": "age", "operator": "lessThan", "value": 40.0}}})
		if out[0]["affectedRows"] != int64(2) || !equal(h.names("id"), []string{"carol"}) {
			t.Fatalf("out %#v names %v", out, h.names("id"))
		}
		if _, err := h.run([]map[string]any{{}}, map[string]any{"operation": "delete", "table": "engi_people"}); err == nil {
			t.Fatal("delete without conditions must fail")
		}
	})

	failing := people(
		map[string]any{"id": 10.0, "name": "ok", "age": 1.0},
		map[string]any{"id": 11.0, "name": nil, "age": 1.0}, // violates NOT NULL
	)
	t.Run("transaction all rolls back on error", func(t *testing.T) {
		h.t = t
		h.reset()
		if _, err := h.run(failing, map[string]any{"operation": "insert", "table": "engi_people", "transaction": "all"}); err == nil {
			t.Fatal("expected error")
		}
		if got := h.names("id"); len(got) != 0 {
			t.Fatalf("rollback left rows: %v", got)
		}
	})
	t.Run("transaction none keeps earlier items", func(t *testing.T) {
		h.t = t
		h.reset()
		if _, err := h.run(failing, map[string]any{"operation": "insert", "table": "engi_people", "transaction": "none"}); err == nil {
			t.Fatal("expected error")
		}
		if got := h.names("id"); !equal(got, []string{"ok"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("transaction per item commits each item", func(t *testing.T) {
		h.t = t
		h.reset()
		if _, err := h.run(failing, map[string]any{"operation": "insert", "table": "engi_people", "transaction": "perItem"}); err == nil {
			t.Fatal("expected error")
		}
		if got := h.names("id"); !equal(got, []string{"ok"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("transaction all commits on success", func(t *testing.T) {
		h.t = t
		h.reset()
		h.must(failing[:1], map[string]any{"operation": "insert", "table": "engi_people", "transaction": "all"})
		if got := h.names("id"); !equal(got, []string{"ok"}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("column types", func(t *testing.T) {
		h.t = t
		h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery", "query": "DELETE FROM engi_types"})
		h.must([]map[string]any{{"id": 1.0, "big": "9007199254740993", "dec": "12.50", "ts": env.Timestamp, "doc": `{"a":[1,2]}`}},
			map[string]any{"operation": "insert", "table": "engi_types"})
		q := map[string]any{"operation": "select", "table": "engi_types"}
		row := h.must([]map[string]any{{}}, q)[0]
		if row["big"] != int64(9007199254740993) || row["dec"] != 12.5 || row["ts"] != "2024-05-06T07:08:09Z" {
			t.Fatalf("default types: %#v", row)
		}
		if doc, _ := row["doc"].(map[string]any); doc == nil || len(doc["a"].([]any)) != 2 {
			t.Fatalf("json column: %#v", row["doc"])
		}
		q["numbersAsString"] = true
		row = h.must([]map[string]any{{}}, q)[0]
		if row["big"] != "9007199254740993" || row["dec"] != "12.50" {
			t.Fatalf("as string: %#v", row)
		}
	})

	t.Run("pool reuse and invalidation", func(t *testing.T) {
		h.t = t
		ctx := context.Background()
		a, err := pools.DB(ctx, env.Driver, h.cred())
		if err != nil {
			t.Fatal(err)
		}
		h.must([]map[string]any{{}}, map[string]any{"operation": "executeQuery", "query": "SELECT 1"})
		b, _ := pools.DB(ctx, env.Driver, h.cred())
		if a != b {
			t.Fatal("pool not reused across node runs")
		}
		if open := a.Stats().OpenConnections; open == 0 || open > 5 {
			t.Fatalf("open connections = %d", open)
		}
		h.ver++ // credential edited
		c, err := pools.DB(ctx, env.Driver, h.cred())
		if err != nil || c == a {
			t.Fatalf("new version must build a new pool (err %v)", err)
		}
		if err := a.PingContext(ctx); err == nil {
			t.Fatal("old pool must be closed")
		}
		if err := c.PingContext(ctx); err != nil {
			t.Fatalf("new pool unusable: %v", err)
		}
	})
}
