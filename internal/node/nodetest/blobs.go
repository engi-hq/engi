package nodetest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// MemoryBlobs is an in-memory nodesdk.BlobAccess for node tests.
type MemoryBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewMemoryBlobs returns an empty store.
func NewMemoryBlobs() *MemoryBlobs { return &MemoryBlobs{data: map[string][]byte{}} }

// Put implements nodesdk.BlobAccess.
func (m *MemoryBlobs) Put(_ context.Context, name, mime string, r io.Reader) (nodesdk.BinaryRef, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nodesdk.BinaryRef{}, fmt.Errorf("reading blob: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := fmt.Sprintf("blob://test/%d", len(m.data))
	m.data[ref] = b
	return nodesdk.BinaryRef{Ref: ref, MIME: mime, Name: name, Size: int64(len(b))}, nil
}

// Open implements nodesdk.BlobAccess.
func (m *MemoryBlobs) Open(_ context.Context, ref nodesdk.BinaryRef) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[ref.Ref]
	if !ok {
		return nil, fmt.Errorf("blob %q: not found", ref.Ref)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// Bytes returns the stored bytes of a reference, for assertions.
func (m *MemoryBlobs) Bytes(ref nodesdk.BinaryRef) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[ref.Ref]
}
