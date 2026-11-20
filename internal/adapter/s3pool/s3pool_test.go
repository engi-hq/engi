package s3pool

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	creds3 "github.com/engi-hq/engi/internal/credtype/s3"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func credential(endpoint string, version int64) nodesdk.Credential {
	return nodesdk.Credential{Type: creds3.Name, ID: "c1", Version: version, Data: map[string]any{
		"endpoint": endpoint, "region": "us-east-1", "accessKeyId": "AKID", "secretAccessKey": "secret",
	}}
}

func TestPoolCachesPerCredentialVersion(t *testing.T) {
	pool := New(Config{ConnectTimeout: time.Second}, quiet)
	ctx := context.Background()
	a, err := pool.Client(ctx, credential("http://h:1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := pool.Client(ctx, credential("http://h:1", 1)); b != a {
		t.Error("same credential version must share a client")
	}
	if c, _ := pool.Client(ctx, credential("http://h:1", 2)); c == a {
		t.Error("a new credential version must get a new client")
	}
	if pool.Len() != 1 {
		t.Errorf("Len = %d, want 1", pool.Len())
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Client(ctx, credential("http://h:1", 2)); err == nil {
		t.Error("closed pool must reject requests")
	}
}

func TestPoolRejectsBadCredential(t *testing.T) {
	pool := New(Config{ConnectTimeout: time.Second}, quiet)
	bad := credential("ftp://h", 1)
	if _, err := pool.Client(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("err = %v, want endpoint error", err)
	}
	bad = credential("", 1)
	delete(bad.Data, "region")
	if _, err := pool.Client(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("err = %v, want region error", err)
	}
}
