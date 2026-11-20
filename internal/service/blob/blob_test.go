package blob_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/blob"
)

// memStore is an in-memory port.BlobStore.
type memStore struct {
	mu    sync.Mutex
	data  map[port.BlobRef][]byte
	meta  map[port.BlobRef]port.BlobInfo
	newer map[string]time.Time
}

func newMem() *memStore {
	return &memStore{data: map[port.BlobRef][]byte{}, meta: map[port.BlobRef]port.BlobInfo{}, newer: map[string]time.Time{}}
}

func (m *memStore) Put(_ context.Context, ref port.BlobRef, info port.BlobInfo, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[ref], m.meta[ref] = b, info
	return int64(len(b)), nil
}

func (m *memStore) Get(_ context.Context, ref port.BlobRef) (io.ReadCloser, port.BlobMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[ref]
	if !ok {
		return nil, port.BlobMeta{}, domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), port.BlobMeta{BlobInfo: m.meta[ref], Size: int64(len(b))}, nil
}

func (m *memStore) Delete(_ context.Context, ref port.BlobRef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, ref)
	return nil
}

func (m *memStore) DeleteExecution(_ context.Context, ex string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for r := range m.data {
		if r.Execution == ex {
			delete(m.data, r)
		}
	}
	return nil
}

func (m *memStore) ExecutionSize(_ context.Context, ex string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for r, b := range m.data {
		if r.Execution == ex {
			n += int64(len(b))
		}
	}
	return n, nil
}

func (m *memStore) Executions(context.Context) ([]port.BlobScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []port.BlobScope
	for r := range m.data {
		if !seen[r.Execution] {
			seen[r.Execution] = true
			out = append(out, port.BlobScope{Execution: r.Execution, Newest: m.newer[r.Execution]})
		}
	}
	return out, nil
}

func service(item, total int64) (*blob.Service, *memStore) {
	cfg := config.Default().Blob
	cfg.MaxItemBytes, cfg.MaxExecutionBytes = item, total
	m := newMem()
	return blob.New(cfg, m), m
}

func TestPutAndOpen(t *testing.T) {
	s, _ := service(100, 1000)
	ref, err := s.Put(context.Background(), "ex_1", "a.txt", "text/plain", strings.NewReader("hello"))
	if err != nil || ref.Size != 5 || !strings.HasPrefix(ref.Ref, "blob://ex_1/") {
		t.Fatalf("Put = %+v, %v", ref, err)
	}
	rc, meta, err := s.Open(context.Background(), ref.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "hello" || meta.Name != "a.txt" {
		t.Errorf("got %q %+v", b, meta)
	}
}

func TestItemLimit(t *testing.T) {
	s, m := service(10, 1000)
	_, err := s.Put(context.Background(), "ex_1", "big", "", strings.NewReader(strings.Repeat("x", 11)))
	if !errors.Is(err, port.ErrBlobTooLarge) {
		t.Fatalf("Put = %v; want ErrBlobTooLarge", err)
	}
	if len(m.data) != 0 {
		t.Error("an oversize value was stored")
	}
	if _, err := s.Put(context.Background(), "ex_1", "ok", "", strings.NewReader(strings.Repeat("x", 10))); err != nil {
		t.Errorf("a value at the limit failed: %v", err)
	}
}

func TestExecutionLimit(t *testing.T) {
	s, _ := service(100, 15)
	put := func(n int) error {
		_, err := s.Put(context.Background(), "ex_1", "f", "", strings.NewReader(strings.Repeat("x", n)))
		return err
	}
	if err := put(10); err != nil {
		t.Fatal(err)
	}
	if err := put(6); !errors.Is(err, port.ErrBlobTooLarge) {
		t.Errorf("second Put = %v; want ErrBlobTooLarge", err)
	}
	if err := put(5); err != nil {
		t.Errorf("Put up to the total failed: %v", err)
	}
	if _, err := s.Put(context.Background(), "ex_2", "f", "", strings.NewReader("x")); err != nil {
		t.Errorf("another execution is limited too: %v", err)
	}
}

func TestOpenRejectsMalformedReferences(t *testing.T) {
	s, _ := service(100, 1000)
	for _, ref := range []string{"", "blob://", "blob://ex_1", "blob://../x", "blob://ex_1/../../x", "file:///etc/passwd", "ex_1/abc"} {
		if _, _, err := s.Open(context.Background(), ref); !errors.Is(err, port.ErrInvalidBlobRef) {
			t.Errorf("Open(%q) = %v; want ErrInvalidBlobRef", ref, err)
		}
	}
}

func TestSweepKeepsLiveAndRecentExecutions(t *testing.T) {
	s, m := service(100, 1000)
	ctx := context.Background()
	now := time.Now()
	for _, ex := range []domain.ExecutionID{"ex_live", "ex_gone", "ex_fresh"} {
		if _, err := s.Put(ctx, ex, "f", "", strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-2 * config.Default().Blob.OrphanGrace)
	m.newer["ex_live"], m.newer["ex_gone"], m.newer["ex_fresh"] = old, old, now
	n, err := s.Sweep(ctx, now, func(_ context.Context, id domain.ExecutionID) (bool, error) { return id == "ex_live", nil })
	if err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want 1", n, err)
	}
	size := func(ex string) int64 { n, _ := m.ExecutionSize(ctx, ex); return n }
	if size("ex_live") == 0 || size("ex_fresh") == 0 || size("ex_gone") != 0 {
		t.Errorf("sizes live=%d fresh=%d gone=%d", size("ex_live"), size("ex_fresh"), size("ex_gone"))
	}
}

func TestScopedAccessStoresUnderTheExecution(t *testing.T) {
	s, _ := service(100, 1000)
	acc := s.Scoped("ex_9")
	ref, err := acc.Put(context.Background(), "n", "text/plain", strings.NewReader("abc"))
	if err != nil || !strings.HasPrefix(ref.Ref, "blob://ex_9/") {
		t.Fatalf("Put = %+v, %v", ref, err)
	}
	rc, err := acc.Open(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}
