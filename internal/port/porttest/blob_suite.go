package porttest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// BlobFactory builds a fresh, empty blob store and registers its cleanup.
type BlobFactory func(t *testing.T) port.BlobStore

// RunBlobStore runs the port.BlobStore contract against an adapter.
func RunBlobStore(t *testing.T, newStore BlobFactory) {
	t.Helper()
	cases := map[string]func(*testing.T, port.BlobStore){
		"PutGetRoundTrip":            blobRoundTrip,
		"LargeObject":                blobLarge,
		"GetMissingIsNotFound":       blobMissing,
		"DeleteIsIdempotent":         blobDelete,
		"DeleteExecutionIsScoped":    blobDeleteExecution,
		"ExecutionSizeSumsBlobs":     blobSize,
		"ExecutionsListsOwners":      blobExecutions,
		"FailedReaderLeavesNothing":  blobFailedReader,
		"UnsafeReferencesRejected":   blobUnsafeRefs,
		"OverwriteReplacesTheBlob":   blobOverwrite,
		"NameWithSeparatorsIsData":   blobHostileName,
		"CanceledContextStopsPut":    blobCanceled,
		"EmptyBlobRoundTrips":        blobEmpty,
		"DeleteExecutionOfNoneIsNil": blobDeleteNone,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, newStore(t)) })
	}
}

func put(t *testing.T, s port.BlobStore, ref port.BlobRef, name, mime string, data []byte) {
	t.Helper()
	n, err := s.Put(context.Background(), ref, port.BlobInfo{Name: name, MIME: mime}, bytes.NewReader(data))
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Put(%s) = %d, %v; want %d, nil", ref, n, err, len(data))
	}
}

func read(t *testing.T, s port.BlobStore, ref port.BlobRef) ([]byte, port.BlobMeta) {
	t.Helper()
	rc, meta, err := s.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get(%s): %v", ref, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading %s: %v", ref, err)
	}
	return b, meta
}

func blobRoundTrip(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a1"}
	put(t, s, ref, "invoice.pdf", "application/pdf", []byte("%PDF-body"))
	b, meta := read(t, s, ref)
	if string(b) != "%PDF-body" || meta.Name != "invoice.pdf" || meta.MIME != "application/pdf" || meta.Size != 9 {
		t.Errorf("got %q %+v", b, meta)
	}
}

func blobLarge(t *testing.T, s port.BlobStore) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB: multipart on s3
	ref := port.BlobRef{Execution: "exe_1", ID: "big"}
	put(t, s, ref, "big.bin", "application/octet-stream", data)
	b, meta := read(t, s, ref)
	if !bytes.Equal(b, data) || meta.Size != int64(len(data)) {
		t.Errorf("large blob differs (size %d, meta %d)", len(b), meta.Size)
	}
}

func blobMissing(t *testing.T, s port.BlobStore) {
	_, _, err := s.Get(context.Background(), port.BlobRef{Execution: "exe_1", ID: "nope"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get missing = %v; want ErrNotFound", err)
	}
}

func blobDelete(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	put(t, s, ref, "a", "text/plain", []byte("x"))
	for i := range 2 {
		if err := s.Delete(context.Background(), ref); err != nil {
			t.Fatalf("Delete #%d: %v", i, err)
		}
	}
	if _, _, err := s.Get(context.Background(), ref); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after delete = %v", err)
	}
}

func blobDeleteExecution(t *testing.T, s port.BlobStore) {
	one, two, other := port.BlobRef{Execution: "exe_1", ID: "a"}, port.BlobRef{Execution: "exe_1", ID: "b"}, port.BlobRef{Execution: "exe_2", ID: "a"}
	for _, r := range []port.BlobRef{one, two, other} {
		put(t, s, r, "n", "text/plain", []byte("x"))
	}
	if err := s.DeleteExecution(context.Background(), "exe_1"); err != nil {
		t.Fatalf("DeleteExecution: %v", err)
	}
	for _, r := range []port.BlobRef{one, two} {
		if _, _, err := s.Get(context.Background(), r); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Get(%s) after DeleteExecution = %v", r, err)
		}
	}
	read(t, s, other)
}

func blobSize(t *testing.T, s port.BlobStore) {
	put(t, s, port.BlobRef{Execution: "exe_1", ID: "a"}, "a", "text/plain", []byte("12345"))
	put(t, s, port.BlobRef{Execution: "exe_1", ID: "b"}, "b", "text/plain", []byte("678"))
	put(t, s, port.BlobRef{Execution: "exe_2", ID: "a"}, "a", "text/plain", []byte("9"))
	n, err := s.ExecutionSize(context.Background(), "exe_1")
	if err != nil || n != 8 {
		t.Errorf("ExecutionSize = %d, %v; want 8", n, err)
	}
	if n, err := s.ExecutionSize(context.Background(), "exe_none"); err != nil || n != 0 {
		t.Errorf("ExecutionSize(none) = %d, %v; want 0", n, err)
	}
}

func blobExecutions(t *testing.T, s port.BlobStore) {
	put(t, s, port.BlobRef{Execution: "exe_1", ID: "a"}, "a", "text/plain", []byte("x"))
	put(t, s, port.BlobRef{Execution: "exe_2", ID: "a"}, "a", "text/plain", []byte("x"))
	scopes, err := s.Executions(context.Background())
	if err != nil || len(scopes) != 2 {
		t.Fatalf("Executions = %v, %v; want 2", scopes, err)
	}
	for _, sc := range scopes {
		if sc.Newest.IsZero() {
			t.Errorf("scope %s has no time", sc.Execution)
		}
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("boom")
	}
	f.n -= len(p)
	return len(p), nil
}

func blobFailedReader(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	_, err := s.Put(context.Background(), ref, port.BlobInfo{}, &failingReader{n: 1 << 10})
	if err == nil {
		t.Fatal("Put with a failing reader succeeded")
	}
	if _, _, err := s.Get(context.Background(), ref); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("failed Put left a blob: %v", err)
	}
	if n, _ := s.ExecutionSize(context.Background(), "exe_1"); n != 0 {
		t.Errorf("failed Put left %d bytes", n)
	}
}

func blobUnsafeRefs(t *testing.T, s port.BlobStore) {
	ctx := context.Background()
	for _, ref := range []port.BlobRef{
		{Execution: "..", ID: "x"}, {Execution: "exe_1", ID: ".."}, {Execution: "exe_1", ID: "../../etc/passwd"},
		{Execution: "a/b", ID: "x"}, {Execution: "", ID: "x"}, {Execution: "exe_1", ID: ""}, {Execution: "exe_1", ID: `a\b`},
	} {
		if _, err := s.Put(ctx, ref, port.BlobInfo{}, bytes.NewReader([]byte("x"))); !errors.Is(err, port.ErrInvalidBlobRef) {
			t.Errorf("Put(%q) = %v; want ErrInvalidBlobRef", ref, err)
		}
		if _, _, err := s.Get(ctx, ref); !errors.Is(err, port.ErrInvalidBlobRef) {
			t.Errorf("Get(%q) = %v; want ErrInvalidBlobRef", ref, err)
		}
		if err := s.Delete(ctx, ref); !errors.Is(err, port.ErrInvalidBlobRef) {
			t.Errorf("Delete(%q) = %v; want ErrInvalidBlobRef", ref, err)
		}
	}
	if err := s.DeleteExecution(ctx, ".."); !errors.Is(err, port.ErrInvalidBlobRef) {
		t.Errorf("DeleteExecution(..) = %v; want ErrInvalidBlobRef", err)
	}
}

func blobOverwrite(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	put(t, s, ref, "one", "text/plain", []byte("first"))
	put(t, s, ref, "two", "text/csv", []byte("second!"))
	if b, meta := read(t, s, ref); string(b) != "second!" || meta.Name != "two" || meta.MIME != "text/csv" {
		t.Errorf("got %q %+v", b, meta)
	}
}

func blobHostileName(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	put(t, s, ref, "../../evil name\x00é.txt", "text/plain", []byte("x"))
	if _, meta := read(t, s, ref); meta.Name != "../../evil name\x00é.txt" {
		t.Errorf("name = %q", meta.Name)
	}
}

func blobCanceled(t *testing.T, s port.BlobStore) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, port.BlobRef{Execution: "exe_1", ID: "a"}, port.BlobInfo{}, bytes.NewReader([]byte("x"))); err == nil {
		t.Error("Put with a canceled context succeeded")
	}
}

func blobEmpty(t *testing.T, s port.BlobStore) {
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	put(t, s, ref, "empty", "text/plain", nil)
	if b, meta := read(t, s, ref); len(b) != 0 || meta.Size != 0 {
		t.Errorf("got %q %+v", b, meta)
	}
}

func blobDeleteNone(t *testing.T, s port.BlobStore) {
	if err := s.DeleteExecution(context.Background(), "exe_none"); err != nil {
		t.Errorf("DeleteExecution(none) = %v", err)
	}
}
