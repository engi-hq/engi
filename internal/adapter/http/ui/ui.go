// Package ui serves the single-page editor that is embedded in the binary.
//
// Requests for files in the bundle get the file; any other GET or HEAD path
// falls back to index.html so client-side routes survive a reload. Hashed files
// under /assets/ are cached for a year, index.html is always revalidated.
// Responses carry the configured Content-Security-Policy.
package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// DefaultCSP is the policy used when none is configured. Inline styles are
// allowed because the code editor and the canvas set style attributes.
const DefaultCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"

const (
	indexName       = "index.html"
	assetsPrefix    = "assets/"
	immutableCache  = "public, max-age=31536000, immutable"
	revalidateCache = "no-cache"
	minGzipBytes    = 1024
)

// Options configures the handler.
type Options struct {
	// ContentSecurityPolicy is sent on every response; empty means DefaultCSP.
	ContentSecurityPolicy string
}

type file struct {
	data []byte
	etag string
	ctyp string

	gzOnce sync.Once
	gz     []byte
}

// gzipped returns the compressed body, computed once on first use.
func (f *file) gzipped() []byte {
	f.gzOnce.Do(func() {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := zw.Write(f.data); err == nil && zw.Close() == nil && buf.Len() < len(f.data) {
			f.gz = buf.Bytes()
		}
	})
	return f.gz
}

type handler struct {
	files map[string]*file
	index *file
	csp   string
}

// New loads the embedded bundle and returns the handler that serves it.
func New(opts Options) (http.Handler, error) {
	root, err := assets()
	if err != nil {
		return nil, fmt.Errorf("opening the embedded ui: %w", err)
	}
	h := &handler{files: map[string]*file{}, csp: opts.ContentSecurityPolicy}
	if h.csp == "" {
		h.csp = DefaultCSP
	}
	err = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		sum := sha256.Sum256(data)
		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.files[p] = &file{data: data, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, ctyp: ct}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("loading the embedded ui: %w", err)
	}
	if h.index = h.files[indexName]; h.index == nil {
		return nil, errors.New("embedded ui has no index.html")
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	f, ok := h.files[name]
	switch {
	case ok:
	case strings.HasPrefix(name, assetsPrefix) || path.Ext(name) != "":
		// A missing file is a 404, not the app shell.
		http.NotFound(w, r)
		return
	default:
		f = h.index
	}
	hd := w.Header()
	hd.Set("Content-Security-Policy", h.csp)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("Content-Type", f.ctyp)
	hd.Set("Vary", "Accept-Encoding")
	hd.Set("ETag", f.etag)
	if strings.HasPrefix(name, assetsPrefix) {
		hd.Set("Cache-Control", immutableCache)
	} else {
		hd.Set("Cache-Control", revalidateCache)
	}
	if r.Header.Get("If-None-Match") == f.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.data
	if len(body) >= minGzipBytes && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && compressible(f.ctyp) {
		if gz := f.gzipped(); gz != nil {
			body = gz
			hd.Set("Content-Encoding", "gzip")
		}
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}
