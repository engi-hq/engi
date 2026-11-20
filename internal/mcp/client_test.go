package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/mcp"
)

// fakeServer plays an MCP server. mode selects the transport and reply style.
type fakeServer struct {
	t        *testing.T
	mode     string // "json", "sse-reply", "legacy"
	session  string
	pageSize int
	failInit int // HTTP status for initialize POSTs; 0 = ok

	mu       sync.Mutex
	seen     []seenRequest
	streams  []chan string // legacy SSE streams
	deleted  bool
	badVer   bool
	toolsets []mcp.ToolInfo
}

type seenRequest struct {
	method, session, version, auth string
}

func (f *fakeServer) tools() []mcp.ToolInfo {
	if f.toolsets != nil {
		return f.toolsets
	}
	return []mcp.ToolInfo{
		{Name: "echo", Description: "Echoes", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "fail", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "rpcerr", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func (f *fakeServer) answer(m mcp.Message) mcp.Message {
	reply := func(v any) mcp.Message {
		b, _ := json.Marshal(v)
		return mcp.Message{JSONRPC: "2.0", ID: m.ID, Result: b}
	}
	switch m.Method {
	case mcp.MethodInitialize:
		ver := mcp.LatestVersion()
		if f.badVer {
			ver = "1999-01-01"
		}
		return reply(mcp.InitializeResult{ProtocolVersion: ver, ServerInfo: mcp.Implementation{Name: "fake"}})
	case mcp.MethodToolsList:
		var p struct{ Cursor string }
		_ = json.Unmarshal(m.Params, &p)
		all := f.tools()
		start := 0
		if p.Cursor != "" {
			fmt.Sscan(p.Cursor, &start)
		}
		end := len(all)
		next := ""
		if f.pageSize > 0 && start+f.pageSize < end {
			end = start + f.pageSize
			next = fmt.Sprint(end)
		}
		return reply(map[string]any{"tools": all[start:end], "nextCursor": next})
	case mcp.MethodToolsCall:
		var p struct {
			Name      string
			Arguments json.RawMessage
		}
		_ = json.Unmarshal(m.Params, &p)
		switch p.Name {
		case "echo":
			return reply(mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: "echo:" + string(p.Arguments)}, {Type: "image", MimeType: "image/png"}}})
		case "fail":
			return reply(mcp.TextResult("it broke", true))
		default:
			return mcp.Message{JSONRPC: "2.0", ID: m.ID, Error: &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "bad tool"}}
		}
	}
	return mcp.Message{JSONRPC: "2.0", ID: m.ID, Error: &mcp.RPCError{Code: mcp.CodeMethodNotFound, Message: "no"}}
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.mode == "legacy" {
		f.serveLegacy(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		f.mu.Lock()
		f.deleted = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var m mcp.Message
	_ = json.Unmarshal(body, &m)
	f.mu.Lock()
	f.seen = append(f.seen, seenRequest{m.Method, r.Header.Get(mcp.HeaderSessionID), r.Header.Get(mcp.HeaderProtocolVersion), r.Header.Get("Authorization")})
	f.mu.Unlock()
	if m.Method == mcp.MethodInitialize && f.failInit != 0 {
		http.Error(w, "nope", f.failInit)
		return
	}
	if m.IsNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if m.Method == mcp.MethodInitialize && f.session != "" {
		w.Header().Set(mcp.HeaderSessionID, f.session)
	}
	resp, _ := json.Marshal(f.answer(m))
	if f.mode == "sse-reply" {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, ": comment\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\nevent: message\ndata: %s\n\n", resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

func (f *fakeServer) serveLegacy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		var m mcp.Message
		_ = json.Unmarshal(body, &m)
		f.mu.Lock()
		f.seen = append(f.seen, seenRequest{m.Method, "", r.Header.Get(mcp.HeaderProtocolVersion), r.Header.Get("Authorization")})
		ch := f.streams[len(f.streams)-1]
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		if m.IsRequest() {
			resp, _ := json.Marshal(f.answer(m))
			ch <- fmt.Sprintf("event: message\ndata: %s\n\n", resp)
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	ch := make(chan string, 8)
	f.mu.Lock()
	f.streams = append(f.streams, ch)
	f.mu.Unlock()
	fmt.Fprint(w, "event: endpoint\ndata: /messages?sessionId=abc\n\n")
	fl.Flush()
	for {
		select {
		case s := <-ch:
			fmt.Fprint(w, s)
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func cfg(url, transport string) mcp.ClientConfig {
	return mcp.ClientConfig{
		Endpoint: url, Transport: transport, Timeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxListPages: 5, MaxTools: 100,
		Info: mcp.Implementation{Name: "engi-test"}, Header: http.Header{"Authorization": {"Bearer secret"}},
	}
}

func connect(t *testing.T, f *fakeServer, transport string) (client *mcp.Client, endpoint string) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, transport))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, srv.URL
}

func TestClientAgainstEveryTransport(t *testing.T) {
	for _, tc := range []struct{ name, mode, transport string }{
		{"streamable json", "json", mcp.TransportStreamableHTTP},
		{"streamable sse reply", "sse-reply", ""},
		{"legacy sse", "legacy", mcp.TransportSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeServer{t: t, mode: tc.mode, pageSize: 2}
			c, _ := connect(t, f, tc.transport)
			tools, err := c.ListTools(context.Background())
			if err != nil || len(tools) != 3 || tools[2].Name != "rpcerr" {
				t.Fatalf("ListTools = %v, %v (pagination must be followed)", tools, err)
			}
			res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"a":1}`))
			if err != nil || res.IsError {
				t.Fatalf("CallTool: %v %+v", err, res)
			}
			if got := res.Text(); !strings.HasPrefix(got, `echo:{"a":1}`) || !strings.Contains(got, "[image content") {
				t.Errorf("Text() = %q", got)
			}
			res, err = c.CallTool(context.Background(), "fail", nil)
			if err != nil || !res.IsError || res.Text() != "it broke" {
				t.Errorf("tool failure = %+v, %v", res, err)
			}
			_, err = c.CallTool(context.Background(), "rpcerr", nil)
			var rpc *mcp.RPCError
			if !errors.As(err, &rpc) || rpc.Code != mcp.CodeInvalidParams {
				t.Errorf("rpc error = %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.seen[0].method != mcp.MethodInitialize || f.seen[1].method != mcp.MethodInitialized {
				t.Errorf("handshake order = %+v", f.seen[:2])
			}
			last := f.seen[len(f.seen)-1]
			if last.version != mcp.LatestVersion() || last.auth != "Bearer secret" {
				t.Errorf("headers on later requests = %+v", last)
			}
		})
	}
}

func TestSessionIDIsSentAndDeleted(t *testing.T) {
	f := &fakeServer{t: t, mode: "json", session: "sess-1"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = c.Close(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen[0].session != "" || f.seen[len(f.seen)-1].session != "sess-1" {
		t.Errorf("session ids = %+v", f.seen)
	}
	if !f.deleted {
		t.Error("Close did not DELETE the session")
	}
}

func TestExpiredSession(t *testing.T) {
	f := &fakeServer{mode: "json", session: "s"}
	var gone bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone && r.Method == http.MethodPost {
			http.NotFound(w, r)
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, ""))
	if err != nil {
		t.Fatal(err)
	}
	gone = true
	if _, err := c.ListTools(context.Background()); !errors.Is(err, mcp.ErrSessionExpired) {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestFallbackToLegacySSE(t *testing.T) {
	legacy := &fakeServer{mode: "legacy"}
	// A legacy server answers POST on the stream URL with 405 (it only serves GET there).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sse" && r.Method == http.MethodPost {
			http.Error(w, "use GET", http.StatusMethodNotAllowed)
			return
		}
		legacy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL+"/sse", ""))
	if err != nil {
		t.Fatalf("Connect with fallback: %v", err)
	}
	defer c.Close(context.Background())
	if tools, err := c.ListTools(context.Background()); err != nil || len(tools) != 3 {
		t.Errorf("ListTools over fallback = %v, %v", tools, err)
	}
}

func TestNoFallbackOnAuthFailure(t *testing.T) {
	f := &fakeServer{mode: "json", failInit: http.StatusUnauthorized}
	srv := httptest.NewServer(f)
	defer srv.Close()
	_, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, ""))
	var status *mcp.HTTPStatusError
	if !errors.As(err, &status) || status.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want HTTP 401", err)
	}
}

func TestUnsupportedServerVersion(t *testing.T) {
	f := &fakeServer{mode: "json", badVer: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	if _, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, "")); err == nil || !strings.Contains(err.Error(), "1999-01-01") {
		t.Fatalf("err = %v", err)
	}
}

func TestListToolsLimits(t *testing.T) {
	f := &fakeServer{mode: "json", pageSize: 1}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := cfg(srv.URL, "")
	c.MaxListPages = 2
	cl, err := mcp.Connect(context.Background(), srv.Client(), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "pages") {
		t.Errorf("page limit err = %v", err)
	}
	c.MaxListPages, c.MaxTools = 10, 2
	cl, _ = mcp.Connect(context.Background(), srv.Client(), c)
	if _, err := cl.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "more than 2 tools") {
		t.Errorf("tool limit err = %v", err)
	}
}

func TestTimeoutAndContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client hanging up
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := cfg(srv.URL, "")
	c.Timeout = 50 * time.Millisecond
	start := time.Now()
	if _, err := mcp.Connect(context.Background(), srv.Client(), c); err == nil {
		t.Fatal("expected a timeout")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("timeout was not honoured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Timeout = time.Minute
	if _, err := mcp.Connect(ctx, srv.Client(), c); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx err = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	for name, mut := range map[string]func(*mcp.ClientConfig){
		"relative endpoint": func(c *mcp.ClientConfig) { c.Endpoint = "/mcp" },
		"ftp":               func(c *mcp.ClientConfig) { c.Endpoint = "ftp://x/y" },
		"bad transport":     func(c *mcp.ClientConfig) { c.Transport = "carrier-pigeon" },
		"zero timeout":      func(c *mcp.ClientConfig) { c.Timeout = 0 },
	} {
		c := cfg("http://localhost:1", "")
		mut(&c)
		if _, err := mcp.Connect(context.Background(), http.DefaultClient, c); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLegacyOtherOriginEndpointRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: endpoint\ndata: http://evil.example/steal\n\n")
	}))
	defer srv.Close()
	if _, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, mcp.TransportSSE)); err == nil || !strings.Contains(err.Error(), "another origin") {
		t.Fatalf("err = %v", err)
	}
}

func TestResponseSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"pad":"` + strings.Repeat("x", 2000) + `"}}`))
	}))
	defer srv.Close()
	c := cfg(srv.URL, "")
	c.MaxResponseBytes = 500
	if _, err := mcp.Connect(context.Background(), srv.Client(), c); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}
