// Package httprpc is the HTTP transport client of the Engi Plugin Protocol:
// every call is one JSON-RPC 2.0 request POSTed to <url>/rpc.
package httprpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// rpcPath is appended to the plugin's base URL.
const rpcPath = "/rpc"

// errBodySnippetBytes bounds the response text quoted in an HTTP error.
const errBodySnippetBytes = 256

// Config describes one HTTP plugin.
type Config struct {
	// Name labels the plugin in errors.
	Name string
	// URL is the plugin's base URL; requests go to URL + "/rpc".
	URL string
	// Token, when set, is sent as a bearer token. It is never logged.
	Token string
	// MaxMessageBytes caps a response body.
	MaxMessageBytes int64
	// CallTimeout bounds one execute call; zero means no extra bound.
	CallTimeout time.Duration
	// HostInfo is sent in initialize.
	HostInfo pluginproto.Info
	// Declared lists the node types the manifest declares.
	Declared []string
}

// Client talks to a plugin service. It implements pluginnode.Caller.
type Client struct {
	cfg    Config
	client *http.Client
	nextID atomic.Int64

	mu      sync.Mutex
	initial *pluginproto.InitializeResult
}

var _ pluginnode.Caller = (*Client)(nil)

// New returns a client that sends requests with client.
func New(cfg Config, client *http.Client) *Client {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &Client{cfg: cfg, client: client}
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type response struct {
	ID     json.RawMessage    `json:"id"`
	Result json.RawMessage    `json:"result"`
	Error  *pluginproto.Error `json:"error"`
}

// Start sends initialize and checks the plugin's descriptors against the
// manifest. It may be called again to re-check; the descriptors must not change.
func (c *Client) Start(ctx context.Context) (pluginproto.InitializeResult, error) {
	var res pluginproto.InitializeResult
	params := pluginproto.InitializeParams{ProtocolVersion: pluginproto.ProtocolVersion, Host: c.cfg.HostInfo}
	if err := c.call(ctx, pluginproto.MethodInitialize, params, &res); err != nil {
		return res, fmt.Errorf("plugin %s: initialize: %w", c.cfg.Name, err)
	}
	if res.ProtocolVersion != pluginproto.ProtocolVersion {
		return res, fmt.Errorf("plugin %s speaks protocol version %d, host requires %d", c.cfg.Name, res.ProtocolVersion, pluginproto.ProtocolVersion)
	}
	for _, n := range res.Nodes {
		if err := pluginproto.CheckDescriptor(n.ToDescriptor(), c.cfg.Declared); err != nil {
			return res, fmt.Errorf("plugin %s: %w", c.cfg.Name, err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initial != nil && !reflect.DeepEqual(c.initial.Nodes, res.Nodes) {
		return res, fmt.Errorf("plugin %s: descriptors changed since the first initialize", c.cfg.Name)
	}
	c.initial = &res
	return res, nil
}

// Execute implements pluginnode.Caller. Callbacks are not available over HTTP,
// so cb is ignored.
func (c *Client) Execute(ctx context.Context, p pluginproto.ExecuteParams, _ pluginnode.Callbacks) (pluginproto.ExecuteResult, error) {
	if c.cfg.CallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.CallTimeout)
		defer cancel()
	}
	var res pluginproto.ExecuteResult
	if err := c.call(ctx, pluginproto.MethodExecute, p, &res); err != nil {
		return res, fmt.Errorf("plugin %s: %w", c.cfg.Name, err)
	}
	return res, nil
}

// Close is a no-op: the plugin's lifetime is not the host's.
func (c *Client) Close(context.Context) error { return nil }

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encoding %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+rpcPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxMessageBytes+1))
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: plugin answered HTTP %d: %s", method, resp.StatusCode, snippet(data))
	}
	if int64(len(data)) > c.cfg.MaxMessageBytes {
		return fmt.Errorf("%s: response: %w (limit %d bytes)", method, pluginproto.ErrMessageTooLarge, c.cfg.MaxMessageBytes)
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("%s: decoding response: %w", method, err)
	}
	if r.Error != nil {
		return r.Error
	}
	if len(r.Result) == 0 {
		return fmt.Errorf("%s: response has neither result nor error", method)
	}
	if err := json.Unmarshal(r.Result, result); err != nil {
		return fmt.Errorf("%s: decoding result: %w", method, err)
	}
	return nil
}

func snippet(b []byte) string {
	if len(b) > errBodySnippetBytes {
		b = b[:errBodySnippetBytes]
	}
	return strings.TrimSpace(string(b))
}
