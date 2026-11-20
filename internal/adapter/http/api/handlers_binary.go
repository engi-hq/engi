package api

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// BinaryReader opens the binary data behind an item reference;
// *blob.Service implements it.
type BinaryReader interface {
	Open(ctx context.Context, ref string) (io.ReadCloser, port.BlobMeta, error)
}

// inlineSafe lists the media types the browser may render in place. Anything
// else, SVG and HTML included, is always a download.
var inlineSafe = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "application/pdf", "text/plain", "audio/", "video/"}

const defaultBinaryMIME = "application/octet-stream"

// getBinary streams one binary value of an execution: GET
// /executions/{id}/binary/{ref}, where ref is the id part of blob://<id>/<ref>.
func (a *API) getBinary(w http.ResponseWriter, r *http.Request) error {
	if a.d.Binary == nil {
		return fmt.Errorf("binary data: %w", ErrNotImplemented)
	}
	id, err := executionID(r)
	if err != nil {
		return err
	}
	ref := port.BlobRef{Execution: string(id), ID: r.PathValue("ref")}
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("binary %q: %w", ref.ID, domain.ErrNotFound)
	}
	rc, meta, err := a.d.Binary.Open(r.Context(), ref.String())
	if err != nil {
		return fmt.Errorf("opening binary data: %w", err)
	}
	defer rc.Close()
	typ := meta.MIME
	if _, _, perr := mime.ParseMediaType(typ); perr != nil {
		typ = defaultBinaryMIME
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "true" && isInlineSafe(typ) {
		disposition = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", typ)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, no-store")
	if meta.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
	if cd := mime.FormatMediaType(disposition, map[string]string{"filename": meta.Name}); cd != "" {
		h.Set("Content-Disposition", cd)
	} else {
		h.Set("Content-Disposition", disposition)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rc) // the status is sent; a broken connection has nobody to tell
	}
	return nil
}

func isInlineSafe(typ string) bool {
	base, _, _ := mime.ParseMediaType(typ)
	for _, ok := range inlineSafe {
		if base == ok || (strings.HasSuffix(ok, "/") && strings.HasPrefix(base, ok)) {
			return true
		}
	}
	return false
}
