// Package blob is the use-case layer over port.BlobStore: it enforces the size
// limits, mints blob ids, scopes node access to one execution and sweeps blobs
// whose execution is gone.
package blob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// idBytes is the random part of a blob id (hex encoded).
const idBytes = 16

// Service stores and reads binary data under the configured limits.
type Service struct {
	store port.BlobStore
	cfg   config.BlobConfig
}

// New returns a Service over store. It is safe for concurrent use.
func New(cfg config.BlobConfig, store port.BlobStore) *Service {
	return &Service{store: store, cfg: cfg}
}

// Put stores r as a new blob of the execution and returns the item reference.
// It fails with port.ErrBlobTooLarge when the value exceeds the item limit or
// pushes the execution over its total.
func (s *Service) Put(ctx context.Context, execution domain.ExecutionID, name, mime string, r io.Reader) (domain.BinaryRef, error) {
	used, err := s.store.ExecutionSize(ctx, string(execution))
	if err != nil {
		return domain.BinaryRef{}, fmt.Errorf("measuring binary data of execution %s: %w", execution, err)
	}
	allowed := min(s.cfg.MaxItemBytes, s.cfg.MaxExecutionBytes-used)
	id, err := newID()
	if err != nil {
		return domain.BinaryRef{}, err
	}
	ref := port.BlobRef{Execution: string(execution), ID: id}
	n, err := s.store.Put(ctx, ref, port.BlobInfo{Name: name, MIME: mime}, &limitReader{r: r, left: allowed, max: s.cfg.MaxItemBytes})
	if err != nil {
		return domain.BinaryRef{}, fmt.Errorf("storing binary data: %w", err)
	}
	return domain.BinaryRef{Ref: ref.String(), MIME: mime, Name: name, Size: n}, nil
}

// Open streams the blob behind a reference.
func (s *Service) Open(ctx context.Context, ref string) (io.ReadCloser, port.BlobMeta, error) {
	r, err := port.ParseBlobRef(ref)
	if err != nil {
		return nil, port.BlobMeta{}, err //nolint:wrapcheck // already descriptive
	}
	rc, meta, err := s.store.Get(ctx, r)
	if err != nil {
		return nil, port.BlobMeta{}, fmt.Errorf("opening binary data: %w", err)
	}
	return rc, meta, nil
}

// DeleteExecution removes every blob of the execution.
func (s *Service) DeleteExecution(ctx context.Context, execution domain.ExecutionID) error {
	if err := s.store.DeleteExecution(ctx, string(execution)); err != nil {
		return fmt.Errorf("deleting binary data of execution %s: %w", execution, err)
	}
	return nil
}

// Sweep deletes the blobs of executions that no longer exist, skipping scopes
// written within the orphan grace (an upload precedes its execution record).
// exists reports whether an execution is still stored. It returns how many
// executions' blobs were removed.
func (s *Service) Sweep(ctx context.Context, now time.Time, exists func(context.Context, domain.ExecutionID) (bool, error)) (int, error) {
	scopes, err := s.store.Executions(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing binary data: %w", err)
	}
	removed := 0
	for _, sc := range scopes {
		if now.Sub(sc.Newest) < s.cfg.OrphanGrace {
			continue
		}
		ok, err := exists(ctx, domain.ExecutionID(sc.Execution))
		if err != nil {
			return removed, fmt.Errorf("checking execution %s: %w", sc.Execution, err)
		}
		if ok {
			continue
		}
		if err := s.store.DeleteExecution(ctx, sc.Execution); err != nil {
			return removed, fmt.Errorf("sweeping execution %s: %w", sc.Execution, err)
		}
		removed++
	}
	return removed, nil
}

// Scoped returns the access nodes of one execution get through Input.Blob().
func (s *Service) Scoped(execution domain.ExecutionID) nodesdk.BlobAccess {
	return scoped{s: s, execution: execution}
}

type scoped struct {
	s         *Service
	execution domain.ExecutionID
}

func (a scoped) Put(ctx context.Context, name, mime string, r io.Reader) (nodesdk.BinaryRef, error) {
	ref, err := a.s.Put(ctx, a.execution, name, mime, r)
	return nodesdk.BinaryRef(ref), err
}

func (a scoped) Open(ctx context.Context, ref nodesdk.BinaryRef) (io.ReadCloser, error) {
	rc, _, err := a.s.Open(ctx, ref.Ref)
	return rc, err
}

func newID() (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating blob id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// limitReader fails with port.ErrBlobTooLarge once more than left bytes are read.
type limitReader struct {
	r    io.Reader
	left int64
	max  int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		l.left = 0
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	if int64(n) > l.left {
		return int(l.left), fmt.Errorf("%w (limit %d bytes per value, %d left in this execution)", port.ErrBlobTooLarge, l.max, l.left)
	}
	l.left -= int64(n)
	return n, err //nolint:wrapcheck // passthrough of the source error
}
