package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Transport names of ClientConfig.Transport.
const (
	// TransportStreamableHTTP is the current transport: JSON-RPC over POST with
	// JSON or SSE replies. A server that answers its initialize with a client
	// error is retried over TransportSSE.
	TransportStreamableHTTP = "streamableHttp"
	// TransportSSE is the legacy HTTP+SSE transport (protocol 2024-11-05).
	TransportSSE = "sse"
)

// Doer is the HTTP client the transports send requests with. *http.Client and
// the node SDK's shared client both satisfy it.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ClientConfig configures Connect. Endpoint is required; the limits must be
// positive (callers take them from configuration).
type ClientConfig struct {
	// Endpoint is the MCP server URL (the single MCP endpoint of Streamable
	// HTTP, or the SSE stream URL of the legacy transport).
	Endpoint string
	// Transport is TransportStreamableHTTP (default when empty) or TransportSSE.
	Transport string
	// Header is sent with every request (authentication).
	Header http.Header
	// Timeout bounds each request/response exchange, and the legacy transport's
	// connection handshake.
	Timeout time.Duration
	// MaxResponseBytes caps one response body, SSE event or stream line.
	MaxResponseBytes int
	// MaxListPages and MaxTools bound tools/list pagination.
	MaxListPages int
	MaxTools     int
	// Info identifies Engi to the server.
	Info Implementation
}

// HTTPStatusError is returned when a server answers a request with an HTTP
// error status.
type HTTPStatusError struct {
	Status int
	// Detail is the start of the response body, for diagnostics.
	Detail string
}

// Error implements error.
func (e *HTTPStatusError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("MCP server answered HTTP %d", e.Status)
	}
	return fmt.Sprintf("MCP server answered HTTP %d: %s", e.Status, e.Detail)
}

// ErrSessionExpired is returned when the server no longer knows the session id
// (HTTP 404 on a request that carried one).
var ErrSessionExpired = errors.New("MCP session expired")

// transport moves JSON-RPC messages to one server.
type transport interface {
	// call sends a request and returns the response with the same id.
	call(ctx context.Context, req Message) (Message, error)
	// notify sends a notification.
	notify(ctx context.Context, n Message) error
	// setVersion records the negotiated protocol version for later requests.
	setVersion(v string)
	// close releases the connection (session, stream, goroutine).
	close(ctx context.Context) error
}

// starter is implemented by transports that must open a connection before the
// first request (the legacy SSE stream).
type starter interface {
	start(ctx context.Context) error
}

// Client talks to one MCP server. Calls may run concurrently. Always Close it.
type Client struct {
	t      transport
	cfg    ClientConfig
	nextID atomic.Int64
	server InitializeResult
}

// Connect opens a session: it initializes the connection and sends
// notifications/initialized. With TransportStreamableHTTP it falls back to the
// legacy SSE transport when the server rejects the initialize POST with a
// client error other than an authentication, timeout or rate-limit one, as the
// spec's backwards-compatibility rules describe.
//
// For the legacy transport ctx also governs the long-lived event stream: it
// must stay alive for the Client's lifetime, and cancelling it ends the stream.
func Connect(ctx context.Context, doer Doer, cfg ClientConfig) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Transport == TransportSSE {
		return connectWith(ctx, newSSE(doer, cfg), cfg)
	}
	c, err := connectWith(ctx, newStreamable(doer, cfg), cfg)
	var status *HTTPStatusError
	if errors.As(err, &status) && fallbackStatus(status.Status) {
		return connectWith(ctx, newSSE(doer, cfg), cfg)
	}
	return c, err
}

func fallbackStatus(code int) bool {
	if code < 400 || code >= 500 {
		return false
	}
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return true
}

func (c ClientConfig) validate() error {
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("MCP endpoint %q is not an absolute http(s) URL", c.Endpoint)
	}
	switch c.Transport {
	case "", TransportStreamableHTTP, TransportSSE:
	default:
		return fmt.Errorf("unknown MCP transport %q", c.Transport)
	}
	if c.Timeout <= 0 || c.MaxResponseBytes <= 0 || c.MaxListPages <= 0 || c.MaxTools <= 0 {
		return errors.New("MCP client limits (timeout, response bytes, list pages, tools) must be positive")
	}
	return nil
}

// connectWith runs the handshake over t and closes t when it fails.
func connectWith(ctx context.Context, t transport, cfg ClientConfig) (*Client, error) {
	c := &Client{t: t, cfg: cfg}
	if s, ok := t.(starter); ok {
		if err := s.start(ctx); err != nil {
			_ = t.close(context.WithoutCancel(ctx))
			return nil, err
		}
	}
	if err := c.initialize(ctx); err != nil {
		_ = t.close(context.WithoutCancel(ctx))
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context) error {
	var res InitializeResult
	err := c.request(ctx, MethodInitialize, initializeParams{
		ProtocolVersion: LatestVersion(), Capabilities: map[string]any{}, ClientInfo: c.cfg.Info,
	}, &res)
	if err != nil {
		return fmt.Errorf("initializing MCP session: %w", err)
	}
	if !SupportedVersion(res.ProtocolVersion) {
		return fmt.Errorf("MCP server speaks protocol %q, which Engi does not support", res.ProtocolVersion)
	}
	c.server = res
	c.t.setVersion(res.ProtocolVersion)
	nctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	if err := c.t.notify(nctx, newNotification(MethodInitialized)); err != nil {
		return fmt.Errorf("sending %s: %w", MethodInitialized, err)
	}
	return nil
}

// Server returns what the server said about itself in initialize.
func (c *Client) Server() InitializeResult { return c.server }

// request sends one request and decodes its result into out (nil to discard).
func (c *Client) request(ctx context.Context, method string, params, out any) error {
	req, err := newRequest(c.nextID.Add(1), method, params)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	resp, err := c.t.call(ctx, req)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("decoding %s result: %w", method, err)
	}
	return nil
}

// ListTools returns every tool of the server, following pagination cursors up
// to the configured page and tool limits (exceeding them is an error rather
// than a silently truncated list).
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var all []ToolInfo
	cursor := ""
	for page := 0; ; page++ {
		if page >= c.cfg.MaxListPages {
			return nil, fmt.Errorf("MCP server lists more than %d pages of tools", c.cfg.MaxListPages)
		}
		var params any
		if cursor != "" {
			params = listToolsParams{Cursor: cursor}
		}
		var res listToolsResult
		if err := c.request(ctx, MethodToolsList, params, &res); err != nil {
			return nil, fmt.Errorf("listing MCP tools: %w", err)
		}
		all = append(all, res.Tools...)
		if len(all) > c.cfg.MaxTools {
			return nil, fmt.Errorf("MCP server offers more than %d tools", c.cfg.MaxTools)
		}
		if res.NextCursor == "" || res.NextCursor == cursor {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

// CallTool runs one tool. args is a JSON object (nil means none). A tool that
// failed has IsError set in the result; a JSON-RPC error is returned as an
// *RPCError and a transport failure as another error.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	var res CallToolResult
	if err := c.request(ctx, MethodToolsCall, callToolParams{Name: name, Arguments: args}, &res); err != nil {
		return CallToolResult{}, fmt.Errorf("calling MCP tool %q: %w", name, err)
	}
	return res, nil
}

// Close ends the session and releases the connection. It is safe to call once;
// the error is informational (the server may not support session deletion).
func (c *Client) Close(ctx context.Context) error {
	return c.t.close(ctx)
}

// Text flattens a tool result into the text a model reads: text parts joined by
// newlines, short placeholders for media, and structuredContent when there are
// no parts at all.
func (r CallToolResult) Text() string {
	var parts []string
	for _, p := range r.Content {
		switch p.Type {
		case "text":
			parts = append(parts, p.Text)
		case "image", "audio":
			parts = append(parts, fmt.Sprintf("[%s content, %s, not shown]", p.Type, orDefault(p.MimeType, "unknown type")))
		case "resource_link":
			parts = append(parts, fmt.Sprintf("[resource %s]", orDefault(p.URI, p.Name)))
		case "resource":
			if p.Resource != nil && p.Resource.Text != "" {
				parts = append(parts, p.Resource.Text)
			} else if p.Resource != nil {
				parts = append(parts, fmt.Sprintf("[resource %s]", p.Resource.URI))
			}
		}
	}
	if len(parts) == 0 && len(r.StructuredContent) > 0 {
		return string(r.StructuredContent)
	}
	return strings.Join(parts, "\n")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
