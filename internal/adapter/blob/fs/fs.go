// Package fs is the blob store on a local directory (standalone mode).
//
// Layout: <dir>/<execution>/<id>.bin holds the bytes and <id>.json the
// metadata. Writes go to a temporary file and are renamed into place, so a
// reader never sees a partial blob.
package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const (
	dataExt = ".bin"
	metaExt = ".json"
	tmpTag  = ".tmp-"
	dirMode = 0o750
)

// Store implements port.BlobStore on a directory.
type Store struct{ dir string }

var _ port.BlobStore = (*Store)(nil)

// New returns a store rooted at dir. The directory is created on first write.
func New(dir string) *Store { return &Store{dir: dir} }

type metaFile struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

func (s *Store) execDir(execution string) string { return filepath.Join(s.dir, execution) }

// paths validates ref and returns its data and metadata paths.
func (s *Store) paths(ref port.BlobRef) (data, meta string, err error) {
	if err := ref.Validate(); err != nil {
		return "", "", err //nolint:wrapcheck // already descriptive
	}
	base := filepath.Join(s.execDir(ref.Execution), ref.ID)
	if !filepath.IsLocal(filepath.Join(ref.Execution, ref.ID)) {
		return "", "", fmt.Errorf("%w: %q", port.ErrInvalidBlobRef, ref.String())
	}
	return base + dataExt, base + metaExt, nil
}

// Put implements port.BlobStore.
func (s *Store) Put(ctx context.Context, ref port.BlobRef, info port.BlobInfo, r io.Reader) (int64, error) {
	data, meta, err := s.paths(ref)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.execDir(ref.Execution), dirMode); err != nil {
		return 0, fmt.Errorf("creating blob directory: %w", err)
	}
	tmp, err := os.CreateTemp(s.execDir(ref.Execution), tmpTag+"*")
	if err != nil {
		return 0, fmt.Errorf("creating temporary blob: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	n, err := io.Copy(tmp, ctxReader{ctx, r})
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("writing blob: %w", err)
	}
	mb, err := json.Marshal(metaFile{Name: info.Name, MIME: info.MIME, Size: n})
	if err != nil {
		return 0, fmt.Errorf("encoding blob metadata: %w", err)
	}
	// Metadata first: a data file without metadata would be unreadable.
	if err := writeAtomic(meta, mb); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), data); err != nil {
		_ = os.Remove(meta)
		return 0, fmt.Errorf("publishing blob: %w", err)
	}
	return n, nil
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tmpTag+"*")
	if err != nil {
		return fmt.Errorf("creating temporary metadata: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing blob metadata: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("publishing blob metadata: %w", err)
	}
	return nil
}

// Get implements port.BlobStore.
func (s *Store) Get(_ context.Context, ref port.BlobRef) (io.ReadCloser, port.BlobMeta, error) {
	data, meta, err := s.paths(ref)
	if err != nil {
		return nil, port.BlobMeta{}, err
	}
	mb, err := os.ReadFile(meta) //nolint:gosec // path built from a validated reference
	if errors.Is(err, fs.ErrNotExist) {
		return nil, port.BlobMeta{}, fmt.Errorf("blob %s: %w", ref, domain.ErrNotFound)
	}
	if err != nil {
		return nil, port.BlobMeta{}, fmt.Errorf("reading blob metadata: %w", err)
	}
	var m metaFile
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, port.BlobMeta{}, fmt.Errorf("decoding blob metadata: %w", err)
	}
	f, err := os.Open(data) //nolint:gosec // path built from a validated reference
	if errors.Is(err, fs.ErrNotExist) {
		return nil, port.BlobMeta{}, fmt.Errorf("blob %s: %w", ref, domain.ErrNotFound)
	}
	if err != nil {
		return nil, port.BlobMeta{}, fmt.Errorf("opening blob: %w", err)
	}
	var created time.Time
	if st, err := f.Stat(); err == nil {
		created = st.ModTime()
	}
	return f, port.BlobMeta{BlobInfo: port.BlobInfo{Name: m.Name, MIME: m.MIME}, Size: m.Size, CreatedAt: created}, nil
}

// Delete implements port.BlobStore.
func (s *Store) Delete(_ context.Context, ref port.BlobRef) error {
	data, meta, err := s.paths(ref)
	if err != nil {
		return err
	}
	for _, p := range []string{data, meta} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("deleting blob %s: %w", ref, err)
		}
	}
	return nil
}

// DeleteExecution implements port.BlobStore.
func (s *Store) DeleteExecution(_ context.Context, execution string) error {
	if err := (port.BlobRef{Execution: execution, ID: "x"}).Validate(); err != nil {
		return err //nolint:wrapcheck // already descriptive
	}
	if err := os.RemoveAll(s.execDir(execution)); err != nil {
		return fmt.Errorf("deleting blobs of execution %s: %w", execution, err)
	}
	return nil
}

// ExecutionSize implements port.BlobStore.
func (s *Store) ExecutionSize(_ context.Context, execution string) (int64, error) {
	if err := (port.BlobRef{Execution: execution, ID: "x"}).Validate(); err != nil {
		return 0, err //nolint:wrapcheck // already descriptive
	}
	entries, err := os.ReadDir(s.execDir(execution))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("listing blobs of execution %s: %w", execution, err)
	}
	var total int64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), dataExt) {
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	return total, nil
}

// Executions implements port.BlobStore.
func (s *Store) Executions(_ context.Context) ([]port.BlobScope, error) {
	dirs, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing blob directory: %w", err)
	}
	var out []port.BlobScope
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		scope := port.BlobScope{Execution: d.Name()}
		if info, err := d.Info(); err == nil {
			scope.Newest = info.ModTime()
		}
		files, _ := os.ReadDir(filepath.Join(s.dir, d.Name()))
		for _, f := range files {
			if info, err := f.Info(); err == nil && info.ModTime().After(scope.Newest) {
				scope.Newest = info.ModTime()
			}
		}
		out = append(out, scope)
	}
	return out, nil
}

// ctxReader stops a copy when the context ends.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // short-lived wrapper around one copy
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, fmt.Errorf("blob write canceled: %w", err)
	}
	return c.r.Read(p) //nolint:wrapcheck // passthrough
}
