//go:build integration

package redis_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/engi-hq/engi/internal/adapter/redispool"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/node/redis"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestRedisIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting redis container: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	pool := redispool.New(redispool.Config{PoolSize: 4, IdleTimeout: time.Minute, MaxLifetime: time.Hour, ConnectTimeout: 5 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = pool.Close() })
	const maxKeys = 3
	n := redis.New(pool, maxKeys)
	cred := nodesdk.Credential{Type: "engi.redis", ID: "cr", Version: 1, Data: map[string]any{"host": host, "port": float64(p.Num())}}

	do := func(t *testing.T, params map[string]any) map[string]any {
		t.Helper()
		in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params)
		in.Credentials = map[string]nodesdk.Credential{"engi.redis": cred}
		out, err := n.Execute(ctx, in)
		if err != nil {
			t.Fatalf("%v: %v", params, err)
		}
		return out.Ports[0][0].JSON
	}

	do(t, map[string]any{"operation": "set", "key": "greeting", "value": "hello"})
	if got := do(t, map[string]any{"operation": "get", "key": "greeting"}); got["value"] != "hello" {
		t.Errorf("get = %v", got)
	}
	if got := do(t, map[string]any{"operation": "get", "key": "missing"}); got["value"] != nil {
		t.Errorf("missing key = %v", got)
	}
	do(t, map[string]any{"operation": "set", "key": "short", "value": "x", "ttl": 100.0})
	rdb, err := pool.Redis(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	if ttl := rdb.TTL(ctx, "short").Val(); ttl <= 0 || ttl > 100*time.Second {
		t.Errorf("ttl = %v, want within (0, 100s]", ttl)
	}
	if got := do(t, map[string]any{"operation": "incr", "key": "n", "amount": 5.0}); got["value"] != 5.0 {
		t.Errorf("incr = %v", got)
	}
	if got := do(t, map[string]any{"operation": "decr", "key": "n", "amount": 2.0}); got["value"] != 3.0 {
		t.Errorf("decr = %v", got)
	}

	do(t, map[string]any{"operation": "hashSet", "key": "h", "fields": map[string]any{"name": "Ada", "age": 36.0}})
	if got := do(t, map[string]any{"operation": "hashGet", "key": "h", "field": "name"}); got["value"] != "Ada" {
		t.Errorf("hashGet field = %v", got)
	}
	all := do(t, map[string]any{"operation": "hashGet", "key": "h"})["value"].(map[string]any)
	if all["age"] != "36" || len(all) != 2 {
		t.Errorf("hashGet all = %v", all)
	}

	do(t, map[string]any{"operation": "listPush", "key": "l", "value": "a", "side": "right"})
	if got := do(t, map[string]any{"operation": "listPush", "key": "l", "value": "b", "side": "right"}); got["length"] != 2.0 {
		t.Errorf("listPush = %v", got)
	}
	if got := do(t, map[string]any{"operation": "listPop", "key": "l", "side": "left"}); got["value"] != "a" {
		t.Errorf("listPop = %v", got)
	}

	for _, k := range []string{"user:1", "user:2", "user:3", "user:4", "user:5"} {
		do(t, map[string]any{"operation": "set", "key": k, "value": "v"})
	}
	res := do(t, map[string]any{"operation": "keys", "pattern": "user:*"})
	keys := res["keys"].([]any)
	if len(keys) != maxKeys || res["truncated"] != true {
		t.Errorf("keys must be capped at %d and flagged truncated: %v", maxKeys, res)
	}
	if res := do(t, map[string]any{"operation": "keys", "pattern": "greet*"}); len(res["keys"].([]any)) != 1 || res["truncated"] != false {
		t.Errorf("keys greet* = %v", res)
	}

	if got := do(t, map[string]any{"operation": "publish", "channel": "ch", "message": "m"}); got["receivers"] != 0.0 {
		t.Errorf("publish = %v", got)
	}
	if got := do(t, map[string]any{"operation": "delete", "key": "greeting"}); got["deleted"] != 1.0 {
		t.Errorf("delete = %v", got)
	}
	if got := do(t, map[string]any{"operation": "get", "key": "greeting"}); got["value"] != nil {
		t.Errorf("deleted key still readable: %v", got)
	}
}
