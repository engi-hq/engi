package mongopool

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestConnectionString(t *testing.T) {
	uri, db, tlsOn, err := ConnectionString(map[string]any{"connectionString": "mongodb://h:1/", "database": "d", "host": "ignored"})
	if err != nil || uri != "mongodb://h:1/" || db != "d" || tlsOn {
		t.Errorf("connection string wins: %q %q %v %v", uri, db, tlsOn, err)
	}
	uri, _, tlsOn, err = ConnectionString(map[string]any{"host": "h", "port": 27018.0, "user": "u", "password": "p@ss/w", "tls": true})
	if err != nil || !tlsOn || !strings.HasPrefix(uri, "mongodb://u:p%40ss%2Fw@h:27018") {
		t.Errorf("built uri %q err %v", uri, err)
	}
	for _, bad := range []map[string]any{{}, {"host": "h", "port": "x"}, {"host": "h", "port": true}} {
		if _, _, _, err := ConnectionString(bad); err == nil {
			t.Errorf("want error for %v", bad)
		}
	}
}

func TestReuseAndInvalidation(t *testing.T) {
	m := New(Config{MaxPoolSize: 3, IdleTimeout: time.Minute, ConnectTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	cred := func(v int64) nodesdk.Credential {
		return nodesdk.Credential{ID: "c", Version: v, Data: map[string]any{"host": "127.0.0.1", "database": "d"}}
	}
	a, db, err := m.Mongo(ctx, cred(1))
	if err != nil || db != "d" {
		t.Fatalf("db=%q err=%v", db, err)
	}
	if b, _, _ := m.Mongo(ctx, cred(1)); a != b || m.Len() != 1 {
		t.Fatal("same version must reuse the client")
	}
	if c, _, _ := m.Mongo(ctx, cred(2)); c == a || m.Len() != 1 {
		t.Fatal("new version must replace the client")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
