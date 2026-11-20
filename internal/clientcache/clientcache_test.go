package clientcache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestVersioningAndClose(t *testing.T) {
	closed := 0
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)), func(int) error { closed++; return nil })
	ctx := context.Background()
	builds := 0
	build := func() (int, error) { builds++; return builds, nil }
	cred := func(v int64) nodesdk.Credential { return nodesdk.Credential{ID: "c1", Version: v} }

	a, _ := c.Get(ctx, "k", cred(1), build)
	b, _ := c.Get(ctx, "k", cred(1), build)
	if a != b || builds != 1 || c.Len() != 1 {
		t.Fatalf("same version must reuse: a=%d b=%d builds=%d", a, b, builds)
	}
	if n, _ := c.Get(ctx, "k", cred(2), build); n == a || closed != 1 || c.Len() != 1 {
		t.Fatalf("new version must rebuild and close the old client (closed=%d)", closed)
	}
	if _, err := c.Get(ctx, "k", nodesdk.Credential{}, build); !errors.Is(err, ErrNoCredentialID) {
		t.Errorf("want ErrNoCredentialID, got %v", err)
	}
	if err := c.Close(); err != nil || closed != 2 || c.Len() != 0 {
		t.Fatalf("close: err=%v closed=%d", err, closed)
	}
	if _, err := c.Get(ctx, "k", cred(2), build); !errors.Is(err, ErrClosed) {
		t.Errorf("want ErrClosed, got %v", err)
	}
}

func TestBuildErrorIsNotCached(t *testing.T) {
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)), func(int) error { return nil })
	boom := errors.New("boom")
	if _, err := c.Get(context.Background(), "k", nodesdk.Credential{ID: "x"}, func() (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if c.Len() != 0 {
		t.Error("failed build must not be cached")
	}
}
