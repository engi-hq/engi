package aitoolmcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/mcp"
	"github.com/engi-hq/engi/internal/node/aitoolmcp"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type tools struct{ list []mcp.ToolInfo }

func (t tools) ListTools(context.Context) ([]mcp.ToolInfo, error) { return t.list, nil }

func (t tools) CallTool(_ context.Context, name string, args json.RawMessage) (mcp.CallToolResult, error) {
	switch name {
	case "echo":
		return mcp.TextResult("got "+string(args), false), nil
	case "fail":
		return mcp.TextResult("kaput", true), nil
	case "long":
		return mcp.TextResult(strings.Repeat("é", 100), false), nil
	}
	return mcp.CallToolResult{}, mcp.ErrUnknownTool
}

func info(names ...string) []mcp.ToolInfo {
	var out []mcp.ToolInfo
	for _, n := range names {
		out = append(out, mcp.ToolInfo{Name: n, Description: "desc " + n})
	}
	return out
}

func newServer(t *testing.T, list []mcp.ToolInfo) (srv *httptest.Server, seen *[]string) {
	t.Helper()
	var auth []string
	s := mcp.NewServer(mcp.Implementation{Name: "t"}, tools{list: list})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		rep := s.Handle(r.Context(), body)
		if rep.Accepted {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rep.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

func node() *aitoolmcp.Node {
	return aitoolmcp.New(aitoolmcp.Config{
		Limits: aitoolmcp.Limits{RequestTimeout: 5 * time.Second, MaxResponseBytes: 1 << 20, MaxListPages: 3, MaxTools: 10, MaxResultBytes: 50},
		Info:   mcp.Implementation{Name: "engi-test"},
	})
}

func supply(t *testing.T, srv *httptest.Server, params map[string]any, creds map[string]nodesdk.Credential) (port.ToolProvider, error) {
	t.Helper()
	n := node()
	params["endpoint"] = srv.URL
	in := nodetest.New(n.Describe(), nil, params)
	in.Client = srv.Client()
	in.Credentials = creds
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		return nil, err
	}
	return v.(port.ToolProvider), nil
}

func names(ts []port.Tool) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Definition().Name)
	}
	return out
}

func TestDescriptorIsAValidSubNode(t *testing.T) {
	d := node().Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.IsSubNode() {
		t.Error("not a sub-node")
	}
}

func TestToolSelection(t *testing.T) {
	srv, _ := newServer(t, info("echo", "fail", "long"))
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"all":        {map[string]any{}, "echo,fail,long"},
		"selected":   {map[string]any{"toolSelection": "selected", "tools": "fail, long"}, "fail,long"},
		"all except": {map[string]any{"toolSelection": "allExcept", "tools": "fail"}, "echo,long"},
	} {
		p, err := supply(t, srv, tc.params, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ts, err := p.Tools(context.Background())
		if err != nil || strings.Join(names(ts), ",") != tc.want {
			t.Errorf("%s: tools = %v, %v", name, names(ts), err)
		}
	}
	p, _ := supply(t, srv, map[string]any{"toolSelection": "selected", "tools": "echo,ghost"}, nil)
	if _, err := p.Tools(context.Background()); err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("missing selected tool err = %v", err)
	}
	if _, err := supply(t, srv, map[string]any{"toolSelection": "selected"}, nil); err == nil {
		t.Error("selected without names should fail")
	}
}

func TestCalls(t *testing.T) {
	srv, _ := newServer(t, info("echo", "fail", "long"))
	p, _ := supply(t, srv, map[string]any{}, nil)
	ts, err := p.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]port.Tool{}
	for _, tl := range ts {
		by[tl.Definition().Name] = tl
	}
	if d := by["echo"].Definition(); d.Description != "desc echo" || string(d.Parameters) != `{"type":"object"}` {
		t.Errorf("definition = %+v", d)
	}
	res, err := by["echo"].Call(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil || res.IsError || res.Content != `got {"x":1}` {
		t.Errorf("echo = %+v, %v", res, err)
	}
	if res, err = by["fail"].Call(context.Background(), nil); err != nil || !res.IsError || res.Content != "kaput" {
		t.Errorf("fail = %+v, %v", res, err)
	}
	res, _ = by["long"].Call(context.Background(), nil)
	if !strings.HasSuffix(res.Content, "[output truncated]") || !strings.HasPrefix(res.Content, "éé") {
		t.Errorf("long = %q", res.Content)
	}
}

func TestRPCErrorBecomesToolError(t *testing.T) {
	srv, _ := newServer(t, info("vanished")) // listed, but the server refuses to run it
	p, _ := supply(t, srv, map[string]any{}, nil)
	ts, _ := p.Tools(context.Background())
	res, err := ts[0].Call(context.Background(), nil)
	if err != nil || !res.IsError || !strings.Contains(res.Content, "unknown tool") {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestToolNamesAreSanitizedAndUnique(t *testing.T) {
	srv, _ := newServer(t, info("a.b", "a b", "a_b", strings.Repeat("x", 80)))
	p, _ := supply(t, srv, map[string]any{}, nil)
	ts, err := p.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := names(ts)
	if got[0] != "a_b" || got[1] != "a_b_2" || got[2] != "a_b_3" || len(got[3]) != 64 {
		t.Errorf("names = %v", got)
	}
	if _, err := port.NewToolset(ts...); err != nil {
		t.Errorf("names are not a valid Toolset: %v", err)
	}
}

func TestAuthentication(t *testing.T) {
	srv, auth := newServer(t, info("echo"))
	creds := map[string]nodesdk.Credential{"engi.mcp": {Data: map[string]any{"authentication": "bearer", "token": "tok"}}}
	p, err := supply(t, srv, map[string]any{"authentication": "credential"}, creds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, a := range *auth {
		if a != "Bearer tok" {
			t.Fatalf("Authorization = %q", a)
		}
	}
	*auth = nil
	p, _ = supply(t, srv, map[string]any{}, creds) // authentication defaults to none: credential ignored
	_, _ = p.Tools(context.Background())
	if len(*auth) == 0 || (*auth)[0] != "" {
		t.Errorf("anonymous request carried %v", *auth)
	}
	if _, err := supply(t, srv, map[string]any{"authentication": "credential"}, nil); err == nil {
		t.Error("expected an error when the credential is missing")
	}
}

func TestHeadersFor(t *testing.T) {
	h, err := aitoolmcp.HeadersFor(map[string]any{"authentication": "header", "headerName": "X-Key", "headerValue": "v"})
	if err != nil || h.Get("X-Key") != "v" {
		t.Errorf("header auth = %v, %v", h, err)
	}
	for name, data := range map[string]map[string]any{
		"bearer no token": {"authentication": "bearer"},
		"crlf token":      {"authentication": "bearer", "token": "a\r\nX: y"},
		"bad header name": {"authentication": "header", "headerName": "bad name", "headerValue": "v"},
		"crlf value":      {"authentication": "header", "headerName": "X", "headerValue": "a\nb"},
		"unknown":         {"authentication": "magic"},
	} {
		if _, err := aitoolmcp.HeadersFor(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if h, err := aitoolmcp.HeadersFor(map[string]any{}); err != nil || len(h) != 0 {
		t.Errorf("empty credential = %v, %v", h, err)
	}
}

func TestSessionsAreClosed(t *testing.T) {
	var deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m mcp.Message
		_ = json.Unmarshal(body, &m)
		if m.Method == mcp.MethodInitialize {
			w.Header().Set(mcp.HeaderSessionID, "s1")
		}
		rep := mcp.NewServer(mcp.Implementation{Name: "t"}, tools{list: info("echo")}).Handle(r.Context(), body)
		if rep.Accepted {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rep.Body)
	}))
	defer srv.Close()
	p, _ := supply(t, srv, map[string]any{}, nil)
	ts, err := p.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts[0].Call(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if deletes != 2 {
		t.Errorf("sessions deleted = %d, want 2 (list + call)", deletes)
	}
}

func TestExecuteIsUnavailable(t *testing.T) {
	n := node()
	if _, err := n.Execute(context.Background(), nil); err == nil {
		t.Error("sub-nodes must not run as steps")
	}
}

// labelled answers every call with its own label, so a test can see which
// server a call reached.
type labelled struct {
	label string
	list  []mcp.ToolInfo
}

func (l labelled) ListTools(context.Context) ([]mcp.ToolInfo, error) { return l.list, nil }

func (l labelled) CallTool(_ context.Context, name string, _ json.RawMessage) (mcp.CallToolResult, error) {
	return mcp.TextResult(l.label+":"+name, false), nil
}

func labelledServer(t *testing.T, h labelled) *httptest.Server {
	t.Helper()
	s := mcp.NewServer(mcp.Implementation{Name: "t"}, h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rep := s.Handle(r.Context(), body)
		if rep.Accepted {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rep.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSameToolNameOnTwoServersGetsDistinctNames(t *testing.T) {
	a := labelledServer(t, labelled{label: "A", list: info("search")})
	b := labelledServer(t, labelled{label: "B", list: info("search")})
	pa, err := supply(t, a, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := supply(t, b, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := pa.(interface {
		WithToolPrefix(string) port.ToolProvider
	}).WithToolPrefix("MCP Server A").Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tb, err := pb.(interface {
		WithToolPrefix(string) port.ToolProvider
	}).WithToolPrefix("MCP Server B").Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]port.Tool{}, ta...), tb...)
	if got := names(all); got[0] != "MCP_Server_A__search" || got[1] != "MCP_Server_B__search" {
		t.Fatalf("names = %v", got)
	}
	ts, err := port.NewToolset(all...)
	if err != nil {
		t.Fatalf("not a valid Toolset: %v", err)
	}
	// Each prefixed name reaches its own server under the server's own name.
	for name, want := range map[string]string{"MCP_Server_A__search": "A:search", "MCP_Server_B__search": "B:search"} {
		res, err := ts.Call(context.Background(), name, nil)
		if err != nil || res.Content != want {
			t.Errorf("%s = %+v, %v; want %q", name, res, err, want)
		}
	}
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func TestToolPrefixIsBounded(t *testing.T) {
	srv := labelledServer(t, labelled{label: "A", list: info("search")})
	p, err := supply(t, srv, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pf := p.(interface {
		WithToolPrefix(string) port.ToolProvider
	})
	for name, node := range map[string]string{
		"blank":       "  ",
		"punctuation": "Mail & Calendar!",
		"long":        strings.Repeat("n", 80),
	} {
		ts, err := pf.WithToolPrefix(node).Tools(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := ts[0].Definition().Name
		if len(got) > 64 || !strings.HasSuffix(got, "__search") || !validName.MatchString(got) {
			t.Errorf("%s: name %q", name, got)
		}
	}
	if ts, _ := pf.WithToolPrefix("  ").Tools(context.Background()); names(ts)[0] != "tool__search" {
		t.Errorf("blank name = %q", names(ts)[0])
	}
	ts, _ := pf.WithToolPrefix("Mail & Calendar!").Tools(context.Background())
	if got := names(ts)[0]; got != "Mail___Calendar___search" {
		t.Errorf("punctuation name = %q", got)
	}
	// Without a prefix the server's name is used unchanged, as before.
	ts, _ = p.Tools(context.Background())
	if got := names(ts)[0]; got != "search" {
		t.Errorf("unprefixed name = %q", got)
	}
}
