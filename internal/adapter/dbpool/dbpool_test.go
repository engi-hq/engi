package dbpool

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func testManager() *Manager {
	return New(Config{MaxOpenConns: 3, MaxIdleConns: 1, IdleTimeout: time.Minute, MaxLifetime: time.Hour, ConnectTimeout: time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func pgCred(version int64) nodesdk.Credential {
	return nodesdk.Credential{Type: "engi.postgres", ID: "cr_1", Version: version,
		Data: map[string]any{"host": "127.0.0.1", "port": 1.0, "database": "d", "user": "u", "password": "p@ss/word"}}
}

func TestReuseAndInvalidation(t *testing.T) {
	m := testManager()
	ctx := context.Background()
	a, err := m.DB(ctx, port.DriverPostgres, pgCred(1))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.DB(ctx, port.DriverPostgres, pgCred(1))
	if a != b || m.Len() != 1 {
		t.Fatal("same credential version must reuse the pool")
	}
	if got := a.Stats().MaxOpenConnections; got != 3 {
		t.Errorf("max open = %d, want 3", got)
	}
	c, _ := m.DB(ctx, port.DriverPostgres, pgCred(2))
	if c == a || m.Len() != 1 {
		t.Fatal("new version must replace the pool")
	}
	if err := a.PingContext(ctx); err == nil || err.Error() != "sql: database is closed" {
		t.Errorf("old pool should be closed, got %v", err)
	}
	other := pgCred(1)
	other.ID = "cr_2"
	if d, _ := m.DB(ctx, port.DriverPostgres, other); d == c || m.Len() != 2 {
		t.Fatal("different credential must get its own pool")
	}
	if err := m.Close(); err != nil || m.Len() != 0 {
		t.Fatalf("close: %v len=%d", err, m.Len())
	}
	if _, err := m.DB(ctx, port.DriverPostgres, pgCred(1)); !errors.Is(err, ErrClosed) {
		t.Errorf("want ErrClosed, got %v", err)
	}
}

func TestRejectsBadCredentials(t *testing.T) {
	m := testManager()
	ctx := context.Background()
	noID := pgCred(1)
	noID.ID = ""
	if _, err := m.DB(ctx, port.DriverPostgres, noID); !errors.Is(err, ErrNoCredentialID) {
		t.Errorf("want ErrNoCredentialID, got %v", err)
	}
	if _, err := m.DB(ctx, "oracle", pgCred(1)); err == nil {
		t.Error("unknown driver must fail")
	}
	missing := nodesdk.Credential{ID: "x", Data: map[string]any{"host": "h"}}
	if _, err := m.DB(ctx, port.DriverMySQL, missing); err == nil {
		t.Error("missing fields must fail")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.DB(cctx, port.DriverPostgres, pgCred(1)); err == nil {
		t.Error("canceled context must fail")
	}
}

func TestMySQLTLSModes(t *testing.T) {
	for mode, wantTLS := range map[string]bool{"": false, "disable": false, "required": true, "skip-verify": true} {
		cfg, err := mysqlTLS(map[string]any{"tlsMode": mode}, "db.example")
		if err != nil || (cfg != nil) != wantTLS {
			t.Errorf("mode %q: cfg=%v err=%v", mode, cfg, err)
		}
	}
	if _, err := mysqlTLS(map[string]any{"tlsMode": "bogus"}, "h"); err == nil {
		t.Error("unknown mode must fail")
	}
	if _, err := mysqlTLS(map[string]any{"tlsMode": "required", "caCertificate": "junk"}, "h"); err == nil {
		t.Error("bad CA must fail")
	}
}
