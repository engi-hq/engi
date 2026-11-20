package pluginsdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

type testNode struct {
	desc nodesdk.Descriptor
	run  func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error)
}

func (n testNode) Describe() nodesdk.Descriptor { return n.desc }
func (n testNode) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	return n.run(ctx, in)
}

func descriptor(typ string) nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: typ, Version: 1, DisplayName: "T", Group: nodesdk.GroupAction,
		Inputs: []nodesdk.PortSpec{{Name: "main"}}, Outputs: []nodesdk.PortSpec{{Name: "main"}},
		Properties: []nodesdk.Property{{Name: "field", Type: nodesdk.PropString}},
	}
}

func upper(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i, it := range in.Items(0) {
		f, err := nodesdk.ParamString(ctx, in, "field", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		s, _ := it.JSON[f].(string)
		out = append(out, nodesdk.Item{JSON: map[string]any{"out": strings.ToUpper(s)}})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func testNodes() []nodesdk.Node {
	return []nodesdk.Node{
		testNode{desc: descriptor("acme.test.upper"), run: upper},
		testNode{desc: descriptor("acme.test.fail"), run: func(context.Context, nodesdk.Input) (nodesdk.Output, error) {
			return nodesdk.Output{}, &nodesdk.NodeError{Message: "nope", HTTPStatus: 418}
		}},
		testNode{desc: descriptor("acme.test.panic"), run: func(context.Context, nodesdk.Input) (nodesdk.Output, error) {
			panic("kaboom")
		}},
		testNode{desc: descriptor("acme.test.callback"), run: func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			v, err := in.EvalExpression(ctx, "1+1", 0)
			if err != nil {
				return nodesdk.Output{}, err
			}
			ref, err := in.Blob().Put(ctx, "a.txt", "text/plain", strings.NewReader("data"))
			if err != nil {
				return nodesdk.Output{}, err
			}
			rc, err := in.Blob().Open(ctx, ref)
			if err != nil {
				return nodesdk.Output{}, err
			}
			b, _ := io.ReadAll(rc)
			in.Logger().Info("hello", "k", "v")
			return nodesdk.Output{Ports: [][]nodesdk.Item{{{JSON: map[string]any{"v": v, "blob": string(b), "ref": ref.Ref}}}}}, nil
		}},
	}
}

func execParams(typ string) pluginproto.ExecuteParams {
	return pluginproto.ExecuteParams{
		CallID: "call1", Node: pluginproto.NodeRef{Type: typ, Version: 1},
		Inputs: [][]pluginproto.Item{{{JSON: map[string]any{"name": "ada"}}}},
		Params: []map[string]any{{"field": "name"}}, ParamErrors: []map[string]string{{}},
	}
}

type host struct {
	conn *pluginproto.Conn
	done chan error
	logs chan pluginproto.LogParams
	mu   sync.Mutex
	seen []string
}

// startHost serves the plugin over pipes and returns a host-side Conn.
func startHost(t *testing.T) *host {
	t.Helper()
	pr, hw := io.Pipe() // host -> plugin
	hr, pw := io.Pipe() // plugin -> host
	h := &host{done: make(chan error, 1), logs: make(chan pluginproto.LogParams, 8)}
	h.conn = pluginproto.NewConn(hr, hw, h.handle, pluginproto.ConnOptions{MaxMessageBytes: 1 << 20, MaxConcurrent: 4})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := pluginsdk.ServeConn(ctx, pr, pw, pluginproto.Info{Name: "acme", Version: "1.0.0"}, pluginsdk.Options{}, testNodes()...)
		_ = pw.Close()
		h.done <- err
	}()
	hostDone := make(chan error, 1)
	go func() { hostDone <- h.conn.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = hw.Close()
		_ = hr.Close()
		<-hostDone
	})
	return h
}

func (h *host) handle(_ context.Context, req pluginproto.Request) (any, error) {
	h.mu.Lock()
	h.seen = append(h.seen, req.Method)
	h.mu.Unlock()
	switch req.Method {
	case pluginproto.MethodLog:
		var p pluginproto.LogParams
		_ = json.Unmarshal(req.Params, &p)
		h.logs <- p
		return nil, nil
	case pluginproto.MethodEvalExpression:
		var p pluginproto.EvalParams
		_ = json.Unmarshal(req.Params, &p)
		if p.CallID != "call1" {
			return nil, &pluginproto.Error{Code: pluginproto.CodeInvalidParams, Message: "bad call"}
		}
		return pluginproto.EvalResult{Value: 2}, nil
	case pluginproto.MethodBlobWrite:
		var p pluginproto.BlobWriteParams
		_ = json.Unmarshal(req.Params, &p)
		return pluginproto.BinaryRef{Ref: "blob://1", Name: p.Name, Size: int64(len(p.Data))}, nil
	case pluginproto.MethodBlobRead:
		return pluginproto.BlobReadResult{Data: []byte("data")}, nil
	}
	return nil, &pluginproto.Error{Code: pluginproto.CodeMethodNotFound, Message: req.Method}
}

func TestServeConn(t *testing.T) {
	h := startHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var init pluginproto.InitializeResult
	if err := h.conn.Call(ctx, pluginproto.MethodInitialize, pluginproto.InitializeParams{ProtocolVersion: 5}, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != 1 || init.Plugin.Name != "acme" || len(init.Nodes) != 4 {
		t.Fatalf("initialize = %+v", init)
	}
	if err := h.conn.Call(ctx, pluginproto.MethodHealth, struct{}{}, nil); err != nil {
		t.Fatal(err)
	}

	t.Run("execute", func(t *testing.T) {
		var res pluginproto.ExecuteResult
		if err := h.conn.Call(ctx, pluginproto.MethodExecute, execParams("acme.test.upper"), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Outputs) != 1 || res.Outputs[0][0].JSON["out"] != "ADA" {
			t.Errorf("outputs = %+v", res.Outputs)
		}
	})
	t.Run("param error surfaces when read", func(t *testing.T) {
		p := execParams("acme.test.upper")
		p.ParamErrors = []map[string]string{{"field": "expression exploded"}}
		delete(p.Params[0], "field")
		err := h.conn.Call(ctx, pluginproto.MethodExecute, p, nil)
		if err == nil || !strings.Contains(err.Error(), "expression exploded") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name     string
			typ      string
			wantCode int
			wantMsg  string
		}{
			{"node error", "acme.test.fail", pluginproto.CodeNodeError, "nope"},
			{"panic", "acme.test.panic", pluginproto.CodeInternalError, "kaboom"},
			{"unknown node", "acme.nothing", pluginproto.CodeInvalidParams, "unknown node"},
		}
		for _, tt := range tests {
			err := h.conn.Call(ctx, pluginproto.MethodExecute, execParams(tt.typ), nil)
			var pe *pluginproto.Error
			if !errors.As(err, &pe) || pe.Code != tt.wantCode || !strings.Contains(pe.Message, tt.wantMsg) {
				t.Errorf("%s: err = %v", tt.name, err)
			}
		}
	})
	t.Run("unknown method", func(t *testing.T) {
		var pe *pluginproto.Error
		if err := h.conn.Call(ctx, "nope", struct{}{}, nil); !errors.As(err, &pe) || pe.Code != pluginproto.CodeMethodNotFound {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("callbacks and logs", func(t *testing.T) {
		var res pluginproto.ExecuteResult
		if err := h.conn.Call(ctx, pluginproto.MethodExecute, execParams("acme.test.callback"), &res); err != nil {
			t.Fatal(err)
		}
		j := res.Outputs[0][0].JSON
		if j["v"] != float64(2) || j["blob"] != "data" || j["ref"] != "blob://1" {
			t.Errorf("json = %v", j)
		}
		select {
		case l := <-h.logs:
			if l.Level != "info" || l.Message != "hello" || l.Attrs["k"] != "v" || l.CallID != "call1" {
				t.Errorf("log = %+v", l)
			}
		case <-ctx.Done():
			t.Fatal("no log received")
		}
	})
	t.Run("shutdown", func(t *testing.T) {
		if err := h.conn.Notify(pluginproto.MethodShutdown, struct{}{}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-h.done:
			if err != nil {
				t.Errorf("ServeConn = %v", err)
			}
		case <-ctx.Done():
			t.Fatal("ServeConn did not return after shutdown")
		}
	})
}

func TestDuplicateNodesRefused(t *testing.T) {
	n := testNode{desc: descriptor("acme.test.upper"), run: upper}
	err := pluginsdk.ServeConn(context.Background(), strings.NewReader(""), io.Discard, pluginproto.Info{}, pluginsdk.Options{}, n, n)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("err = %v", err)
	}
}

func rpc(t *testing.T, url, method string, params any) (status int, body map[string]any) {
	t.Helper()
	reqBody, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	resp, err := http.Post(url+"/rpc", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHandler(t *testing.T) {
	srv := httptest.NewServer(pluginsdk.Handler(pluginproto.Info{Name: "acme"}, pluginsdk.Options{MaxMessageBytes: 4096}, testNodes()...))
	defer srv.Close()

	code, out := rpc(t, srv.URL, "execute", execParams("acme.test.upper"))
	if code != 200 || out["result"] == nil {
		t.Fatalf("execute: %d %v", code, out)
	}
	_, out = rpc(t, srv.URL, "execute", execParams("acme.test.callback"))
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil || int(errObj["code"].(float64)) != pluginproto.CodeUnsupported {
		t.Errorf("callback over HTTP: %v", out)
	}
	_, out = rpc(t, srv.URL, "execute", execParams("acme.test.fail"))
	if errObj, _ = out["error"].(map[string]any); errObj == nil || int(errObj["code"].(float64)) != pluginproto.CodeNodeError {
		t.Errorf("node error: %v", out)
	}

	tests := []struct {
		name, method, path string
		want               int
	}{
		{"GET on rpc", http.MethodGet, "/rpc", http.StatusMethodNotAllowed},
		{"other path", http.MethodPost, "/other", http.StatusNotFound},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s: status = %d", tt.name, resp.StatusCode)
		}
	}
	resp, err := http.Post(srv.URL+"/rpc", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	var bad map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&bad)
	_ = resp.Body.Close()
	if e, _ := bad["error"].(map[string]any); e == nil || int(e["code"].(float64)) != pluginproto.CodeParseError {
		t.Errorf("parse error = %v", bad)
	}
}
