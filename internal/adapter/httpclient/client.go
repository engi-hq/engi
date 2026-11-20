// Package httpclient implements nodesdk.HTTPClient on top of net/http with the
// timeouts, size limits, proxy and TLS settings from configuration.
package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ErrResponseTooLarge is returned by a response body read that exceeds the
// configured limit.
var ErrResponseTooLarge = errors.New("http response body exceeds the size limit")

// traceparentHeader is the W3C trace context header.
const traceparentHeader = "traceparent"

// Client is a shared outbound HTTP client. It is safe for concurrent use.
type Client struct {
	cfg    config.HTTPClientConfig
	http   *http.Client
	tracer port.Tracer
}

// Option customizes a Client.
type Option func(*Client)

// WithTracer traces each outbound request as a client span and propagates the
// W3C trace context in the traceparent header. Spans carry the method, host
// and status only, never the path, query, headers or body.
func WithTracer(t port.Tracer) Option {
	return func(c *Client) {
		if t != nil {
			c.tracer = t
		}
	}
}

var _ nodesdk.HTTPClient = (*Client)(nil)

// New builds a Client. readFile loads the optional CA bundle; pass os.ReadFile
// from the composition root (nil defaults to os.ReadFile).
func New(cfg config.HTTPClientConfig, readFile func(string) ([]byte, error), opts ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid http client config: %w", err)
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	tlsCfg, err := tlsConfig(cfg, readFile)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: cfg.DialTimeout}).DialContext,
		TLSHandshakeTimeout: cfg.TLSHandshakeTimeout,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		MaxIdleConns:        cfg.MaxIdleConns,
		TLSClientConfig:     tlsCfg,
		ForceAttemptHTTP2:   true,
	}
	if cfg.ProxyURL != "" {
		u, err := url.Parse(cfg.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("parsing proxy url: %w", err)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	c := &Client{cfg: cfg, tracer: port.NopTracer{}}
	for _, o := range opts {
		o(c)
	}
	c.http = &http.Client{Transport: tr, CheckRedirect: c.checkRedirect}
	return c, nil
}

func tlsConfig(cfg config.HTTPClientConfig, readFile func(string) ([]byte, error)) (*tls.Config, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecureSkipVerify} //nolint:gosec // explicit opt-in via configuration
	if cfg.TLSCAFile == "" {
		return t, nil
	}
	pem, err := readFile(cfg.TLSCAFile)
	if err != nil {
		return nil, fmt.Errorf("reading TLS CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("TLS CA file %q contains no certificates", cfg.TLSCAFile)
	}
	t.RootCAs = pool
	return t, nil
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if nodesdk.HTTPCallOptionsFrom(req.Context()).NoRedirect {
		return http.ErrUseLastResponse
	}
	if len(via) > c.cfg.MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", c.cfg.MaxRedirects)
	}
	return nil
}

// Do sends the request. A request whose context has no deadline gets the
// configured timeout, which also covers reading the body. The body of the
// response is capped at the configured size; reading past it fails with
// ErrResponseTooLarge. A User-Agent is added unless the request has one.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	cancel := context.CancelFunc(func() {})
	if _, ok := req.Context().Deadline(); !ok {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(req.Context(), c.cfg.Timeout)
		req = req.WithContext(ctx)
	}
	if req.Header.Get("User-Agent") == "" && c.cfg.UserAgent != "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	ctx, span := c.tracer.Start(req.Context(), "HTTP "+req.Method,
		port.StrAttr("http.request.method", req.Method), port.StrAttr("server.address", req.URL.Hostname()))
	defer span.End()
	if tp := c.tracer.Inject(ctx); tp != "" {
		req = req.Clone(ctx)
		req.Header.Set(traceparentHeader, tp)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		span.Fail("request failed")
		return nil, fmt.Errorf("sending %s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	span.SetAttrs(port.IntAttr("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusInternalServerError {
		span.Fail("server error")
	}
	resp.Body = &limitedBody{rc: resp.Body, remaining: c.cfg.MaxResponseBytes, cancel: cancel}
	return resp, nil
}

// limitedBody enforces the size limit and releases the request context on Close.
type limitedBody struct {
	rc        io.ReadCloser
	remaining int64
	cancel    context.CancelFunc
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, ErrResponseTooLarge
	}
	// Read one byte more than allowed so that exceeding the limit is detectable.
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return int(int64(n) + b.remaining), ErrResponseTooLarge
	}
	return n, err //nolint:wrapcheck // passthrough of the transport's read result
}

func (b *limitedBody) Close() error {
	defer b.cancel()
	return b.rc.Close() //nolint:wrapcheck // passthrough
}
