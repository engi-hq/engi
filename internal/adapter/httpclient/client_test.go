package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func newClient(t *testing.T, mutate func(*config.HTTPClientConfig)) *Client {
	t.Helper()
	cfg := config.Default().HTTPClient
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func get(t *testing.T, c *Client, ctx context.Context, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

func TestUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.UserAgent()))
	}))
	defer srv.Close()
	c := newClient(t, func(c *config.HTTPClientConfig) { c.UserAgent = "engi-test" })

	resp, err := get(t, c, context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "engi-test" {
		t.Fatalf("user agent = %q", body)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	req.Header.Set("User-Agent", "mine")
	resp2, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ = io.ReadAll(resp2.Body)
	if string(body) != "mine" {
		t.Fatalf("explicit user agent overridden: %q", body)
	}
}

func TestMaxResponseBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()
	tests := []struct {
		name    string
		limit   int64
		wantErr bool
	}{
		{"under", 1000, false},
		{"exact", 100, false},
		{"over", 99, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(t, func(c *config.HTTPClientConfig) { c.MaxResponseBytes = tt.limit })
			resp, err := get(t, c, context.Background(), srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if tt.wantErr {
				if !errors.Is(err, ErrResponseTooLarge) {
					t.Fatalf("err = %v, want ErrResponseTooLarge", err)
				}
				return
			}
			if err != nil || len(body) != 100 {
				t.Fatalf("len=%d err=%v", len(body), err)
			}
		})
	}
}

func TestDefaultTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	c := newClient(t, func(c *config.HTTPClientConfig) { c.Timeout = 50 * time.Millisecond })
	if _, err := get(t, c, context.Background(), srv.URL); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/end", http.StatusFound) })
	mux.HandleFunc("/end", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("done")) })
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newClient(t, func(c *config.HTTPClientConfig) { c.MaxRedirects = 3 })

	resp, err := get(t, c, context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want followed redirect", resp.StatusCode)
	}

	ctx := nodesdk.WithHTTPCallOptions(context.Background(), nodesdk.HTTPCallOptions{NoRedirect: true})
	resp, err = get(t, c, ctx, srv.URL+"/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 when redirects are disabled", resp.StatusCode)
	}

	if _, err := get(t, c, context.Background(), srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("err = %v, want redirect limit error", err)
	}
}

func TestProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("via proxy " + r.Host))
	}))
	defer proxy.Close()
	c := newClient(t, func(c *config.HTTPClientConfig) { c.ProxyURL = proxy.URL })
	resp, err := get(t, c, context.Background(), "http://example.invalid/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "via proxy example.invalid" {
		t.Fatalf("body = %q", body)
	}
}

func TestTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()

	if _, err := get(t, newClient(t, nil), context.Background(), srv.URL); err == nil {
		t.Fatal("self-signed certificate must be rejected by default")
	}
	c := newClient(t, func(c *config.HTTPClientConfig) { c.TLSInsecureSkipVerify = true })
	resp, err := get(t, c, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("insecure skip verify: %v", err)
	}
	resp.Body.Close()
}

func TestNewRejectsBadConfig(t *testing.T) {
	cfg := config.Default().HTTPClient
	cfg.ProxyURL = "not a url"
	if _, err := New(cfg, nil); err == nil {
		t.Fatal("expected proxy validation error")
	}
	cfg = config.Default().HTTPClient
	cfg.TLSCAFile = "ca.pem"
	if _, err := New(cfg, func(string) ([]byte, error) { return []byte("junk"), nil }); err == nil {
		t.Fatal("expected CA bundle error")
	}
	if _, err := New(cfg, func(string) ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("expected read error")
	}
}
