package ui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h, err := New(Options{ContentSecurityPolicy: "default-src 'none'"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, http.NoBody)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIndexAndFallback(t *testing.T) {
	for _, target := range []string{"/", "/index.html", "/workflows/wf_1", "/executions"} {
		rec := serve(t, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Engi</title>") {
			t.Fatalf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != revalidateCache {
			t.Errorf("%s cache-control = %q", target, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("Content-Security-Policy") != "default-src 'none'" {
			t.Errorf("%s csp = %q", target, rec.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestMissingFilesAre404(t *testing.T) {
	for _, target := range []string{"/assets/missing.js", "/favicon.ico", "/x.css"} {
		if rec := serve(t, http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d", target, rec.Code)
		}
	}
}

func TestMethodsAndConditional(t *testing.T) {
	if rec := serve(t, http.MethodPost, "/", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	etag := serve(t, http.MethodGet, "/", nil).Header().Get("ETag")
	if rec := serve(t, http.MethodGet, "/", map[string]string{"If-None-Match": etag}); rec.Code != http.StatusNotModified {
		t.Errorf("conditional = %d", rec.Code)
	}
}

func TestGzip(t *testing.T) {
	f := &file{data: []byte(strings.Repeat("engi ", 1000))}
	gz := f.gzipped()
	if gz == nil || len(gz) >= len(f.data) {
		t.Fatalf("gzip = %d bytes", len(gz))
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, f.data) {
		t.Fatal("round trip mismatch")
	}
}
