package nodesdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrUnavailable is returned by the placeholder services used when the host
// has not configured the real ones.
var ErrUnavailable = errors.New("service is not configured")

// UnavailableBlob is a BlobAccess whose every call fails with ErrUnavailable.
type UnavailableBlob struct{}

// Put implements BlobAccess.
func (UnavailableBlob) Put(context.Context, string, string, io.Reader) (BinaryRef, error) {
	return BinaryRef{}, fmt.Errorf("blob store: %w", ErrUnavailable)
}

// Open implements BlobAccess.
func (UnavailableBlob) Open(context.Context, BinaryRef) (io.ReadCloser, error) {
	return nil, fmt.Errorf("blob store: %w", ErrUnavailable)
}

// UnavailableHTTP is an HTTPClient whose every call fails with ErrUnavailable.
type UnavailableHTTP struct{}

// Do implements HTTPClient.
func (UnavailableHTTP) Do(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("http client: %w", ErrUnavailable)
}
