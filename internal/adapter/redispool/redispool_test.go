package redispool

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestOptions(t *testing.T) {
	o, err := Options(map[string]any{"host": "r.example", "port": 6380.0, "db": "2", "username": "u", "password": "p", "tls": true})
	if err != nil {
		t.Fatal(err)
	}
	if o.Addr != "r.example:6380" || o.DB != 2 || o.Username != "u" || o.Password != "p" || o.TLSConfig == nil {
		t.Errorf("unexpected options %+v", o)
	}
	d, _ := Options(map[string]any{})
	if d.Addr != "localhost:6379" || d.TLSConfig != nil {
		t.Errorf("defaults wrong: %+v", d)
	}
	for _, bad := range []map[string]any{{"port": "x"}, {"db": -1.0}, {"port": true}} {
		if _, err := Options(bad); err == nil {
			t.Errorf("want error for %v", bad)
		}
	}
}

func TestReuseAndInvalidation(t *testing.T) {
	m := New(Config{PoolSize: 2, IdleTimeout: time.Minute, MaxLifetime: time.Hour, ConnectTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	cred := func(v int64) nodesdk.Credential {
		return nodesdk.Credential{ID: "c", Version: v, Data: map[string]any{"host": "127.0.0.1"}}
	}
	a, _ := m.Redis(ctx, cred(1))
	b, _ := m.Redis(ctx, cred(1))
	if a != b || m.Len() != 1 || a.Options().PoolSize != 2 {
		t.Fatal("same version must reuse the client")
	}
	if c, _ := m.Redis(ctx, cred(2)); c == a || m.Len() != 1 {
		t.Fatal("new version must replace the client")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
