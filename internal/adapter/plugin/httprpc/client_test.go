package httprpc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/plugin/httprpc"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

type upperNode struct{ typ string }

func (n upperNode) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: n.typ, Version: 1, DisplayName: "Upper", Group: nodesdk.GroupTransform,
		Inputs: []nodesdk.PortSpec{{Name: "main"}}, Outputs: []nodesdk.PortSpec{{Name: "main"}},
	}
}

func (upperNode) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for _, it := range in.Items(0) {
		s, _ := it.JSON["s"].(string)
		out = append(out, nodesdk.Item{JSON: map[string]any{"s": strings.ToUpper(s)}})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

type bigNode struct{ upperNode }

func (bigNode) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{Ports: [][]nodesdk.Item{{{JSON: map[string]any{"s": strings.Repeat("x", 10_000)}}}}}, nil
}

func newServer(t *testing.T, token string, nodes ...nodesdk.Node) *httptest.Server {
	t.Helper()
	h := pluginsdk.Handler(pluginproto.Info{Name: "acme", Version: "1"}, pluginsdk.Options{}, nodes...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func cfg(url string) httprpc.Config {
	return httprpc.Config{Name: "acme", URL: url, MaxMessageBytes: 4096, CallTimeout: 5 * time.Second,
		HostInfo: pluginproto.Info{Name: "engi"}, Declared: []string{"acme.up", "acme.big"}}
}

func params(typ string) pluginproto.ExecuteParams {
	return pluginproto.ExecuteParams{
		CallID: "c", Node: pluginproto.NodeRef{Type: typ, Version: 1},
		Inputs: [][]pluginproto.Item{{{JSON: map[string]any{"s": "abc"}}}}, Params: []map[string]any{{}},
	}
}

func TestRoundTripAndToken(t *testing.T) {
	srv := newServer(t, "s3cret", upperNode{"acme.up"})
	ctx := context.Background()

	c := cfg(srv.URL + "/") // trailing slash is tolerated
	c.Token = "s3cret"
	cl := httprpc.New(c, srv.Client())
	res, err := cl.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plugin.Name != "acme" || len(res.Nodes) != 1 {
		t.Fatalf("start = %+v", res)
	}
	out, err := cl.Execute(ctx, params("acme.up"), nil)
	if err != nil || out.Outputs[0][0].JSON["s"] != "ABC" {
		t.Fatalf("execute = %+v, %v", out, err)
	}
	if err := cl.Close(ctx); err != nil {
		t.Error(err)
	}

	c.Token = "wrong"
	_, err = httprpc.New(c, srv.Client()).Execute(ctx, params("acme.up"), nil)
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong") {
		t.Errorf("wrong token: err = %v", err)
	}
}

func TestFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("oversized response", func(t *testing.T) {
		srv := newServer(t, "", bigNode{upperNode{"acme.big"}})
		_, err := httprpc.New(cfg(srv.URL), srv.Client()).Execute(ctx, params("acme.big"), nil)
		if !errors.Is(err, pluginproto.ErrMessageTooLarge) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("http 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "db down", http.StatusInternalServerError)
		}))
		defer srv.Close()
		_, err := httprpc.New(cfg(srv.URL), srv.Client()).Execute(ctx, params("acme.up"), nil)
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "db down") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("jsonrpc error", func(t *testing.T) {
		srv := newServer(t, "", upperNode{"acme.up"})
		_, err := httprpc.New(cfg(srv.URL), srv.Client()).Execute(ctx, params("acme.nothing"), nil)
		var pe *pluginproto.Error
		if !errors.As(err, &pe) || pe.Code != pluginproto.CodeInvalidParams {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("start rejects undeclared type", func(t *testing.T) {
		srv := newServer(t, "", upperNode{"acme.other"})
		_, err := httprpc.New(cfg(srv.URL), srv.Client()).Start(ctx)
		if err == nil || !strings.Contains(err.Error(), "not listed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("start rejects protocol version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2,"plugin":{},"nodes":[]}}`))
		}))
		defer srv.Close()
		_, err := httprpc.New(cfg(srv.URL), srv.Client()).Start(ctx)
		if err == nil || !strings.Contains(err.Error(), "protocol version") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("call timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		defer srv.Close()
		defer close(release)
		c := cfg(srv.URL)
		c.CallTimeout = 50 * time.Millisecond
		_, err := httprpc.New(c, srv.Client()).Execute(ctx, params("acme.up"), nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v", err)
		}
	})
}
