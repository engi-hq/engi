package port

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Blob errors; check with errors.Is. A missing blob wraps domain.ErrNotFound.
var (
	// ErrBlobTooLarge: a value or an execution exceeded a configured size limit.
	ErrBlobTooLarge = errors.New("binary data exceeds the configured size limit")
	// ErrInvalidBlobRef: the reference is not of the form blob://<execution>/<id>.
	ErrInvalidBlobRef = errors.New("invalid binary reference")
)

const blobScheme = "blob://"

// blobPart restricts both reference segments to a safe alphabet, so a
// reference can never address anything outside its store (no dots, no slashes).
var blobPart = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// BlobRef addresses one blob: the execution that owns it and its id.
type BlobRef struct {
	Execution string
	ID        string
}

// String renders blob://<execution>/<id>.
func (r BlobRef) String() string { return blobScheme + r.Execution + "/" + r.ID }

// Validate reports whether both segments are safe.
func (r BlobRef) Validate() error {
	if !blobPart.MatchString(r.Execution) || !blobPart.MatchString(r.ID) {
		return fmt.Errorf("%w: %q", ErrInvalidBlobRef, r.String())
	}
	return nil
}

// ParseBlobRef parses and validates a reference string.
func ParseBlobRef(s string) (BlobRef, error) {
	rest, ok := strings.CutPrefix(s, blobScheme)
	if !ok {
		return BlobRef{}, fmt.Errorf("%w: %q", ErrInvalidBlobRef, s)
	}
	exec, id, ok := strings.Cut(rest, "/")
	if !ok {
		return BlobRef{}, fmt.Errorf("%w: %q", ErrInvalidBlobRef, s)
	}
	ref := BlobRef{Execution: exec, ID: id}
	if err := ref.Validate(); err != nil {
		return BlobRef{}, err
	}
	return ref, nil
}

// BlobInfo is the descriptive metadata stored beside the bytes.
type BlobInfo struct {
	Name string
	MIME string
}

// BlobMeta describes a stored blob.
type BlobMeta struct {
	BlobInfo
	Size      int64
	CreatedAt time.Time
}

// BlobScope summarises the blobs of one execution, for the orphan sweep.
type BlobScope struct {
	Execution string
	// Newest is the modification time of the most recently written blob.
	Newest time.Time
}

// BlobStore holds the bytes behind binary item fields. Size limits are the
// caller's concern (see service/blob); a store only has to fail cleanly, and
// leave nothing behind, when the reader it streams from fails.
type BlobStore interface {
	// Put streams r into a new blob and returns the byte count.
	Put(ctx context.Context, ref BlobRef, info BlobInfo, r io.Reader) (int64, error)
	// Get opens the blob; a missing one wraps domain.ErrNotFound.
	Get(ctx context.Context, ref BlobRef) (io.ReadCloser, BlobMeta, error)
	// Delete removes one blob; a missing one is not an error.
	Delete(ctx context.Context, ref BlobRef) error
	// DeleteExecution removes every blob of the execution.
	DeleteExecution(ctx context.Context, execution string) error
	// ExecutionSize sums the bytes stored for the execution.
	ExecutionSize(ctx context.Context, execution string) (int64, error)
	// Executions lists the executions that own blobs.
	Executions(ctx context.Context) ([]BlobScope, error)
}
