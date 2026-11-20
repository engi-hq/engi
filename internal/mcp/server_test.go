package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/mcp"
)

type stubTools struct{}

func (stubTools) ListTools(context.Context) ([]mcp.ToolInfo, error) {
	return []mcp.ToolInfo{{Name: "add", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
}

func (stubTools) CallTool(_ context.Context, name string, args json.RawMessage) (mcp.CallToolResult, error) {
	switch name {
	case "add":
		return mcp.TextResult("sum "+string(args), false), nil
	case "busy":
		return mcp.CallToolResult{}, &mcp.RPCError{Code: mcp.CodeServerBusy, Message: "busy"}
	case "boom":
		return mcp.CallToolResult{}, errors.New("secret internal detail")
	}
	return mcp.CallToolResult{}, mcp.ErrUnknownTool
}

func handle(t *testing.T, in string) (rep mcp.Reply, body map[string]any) {
	t.Helper()
	rep = mcp.NewServer(mcp.Implementation{Name: "engi"}, stubTools{}).Handle(context.Background(), []byte(in))
	if len(rep.Body) > 0 && rep.Body[0] == '{' {
		if err := json.Unmarshal(rep.Body, &body); err != nil {
			t.Fatalf("reply is not JSON: %s", rep.Body)
		}
	}
	return rep, body
}

func TestServerDispatch(t *testing.T) {
	t.Run("initialize negotiates", func(t *testing.T) {
		for in, want := range map[string]string{"2025-03-26": "2025-03-26", "2099-01-01": mcp.LatestVersion()} {
			_, out := handle(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+in+`","capabilities":{},"clientInfo":{"name":"c"}}}`)
			res := out["result"].(map[string]any)
			if res["protocolVersion"] != want || res["serverInfo"].(map[string]any)["name"] != "engi" {
				t.Errorf("initialize(%s) = %v", in, res)
			}
			if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
				t.Error("tools capability missing")
			}
		}
	})
	t.Run("notification gets 202", func(t *testing.T) {
		if r, _ := handle(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); !r.Accepted || len(r.Body) != 0 {
			t.Errorf("reply = %+v", r)
		}
	})
	t.Run("ping", func(t *testing.T) {
		if _, out := handle(t, `{"jsonrpc":"2.0","id":"p","method":"ping"}`); out["id"] != "p" || out["error"] != nil {
			t.Errorf("ping = %v", out)
		}
	})
	t.Run("list and call", func(t *testing.T) {
		_, out := handle(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
		if tools := out["result"].(map[string]any)["tools"].([]any); len(tools) != 1 {
			t.Errorf("tools = %v", tools)
		}
		_, out = handle(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":1}}}`)
		if got := out["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; got != `sum {"a":1}` {
			t.Errorf("call = %v", got)
		}
	})
	t.Run("errors", func(t *testing.T) {
		for name, tc := range map[string]struct {
			body string
			code float64
		}{
			"unknown method": {`{"jsonrpc":"2.0","id":1,"method":"nope"}`, mcp.CodeMethodNotFound},
			"unknown tool":   {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"zip"}}`, mcp.CodeInvalidParams},
			"no tool name":   {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, mcp.CodeInvalidParams},
			"handler rpc":    {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"busy"}}`, mcp.CodeServerBusy},
			"handler other":  {`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom"}}`, mcp.CodeInternalError},
			"wrong version":  {`{"jsonrpc":"1.0","id":1,"method":"ping"}`, mcp.CodeInvalidRequest},
		} {
			r, out := handle(t, tc.body)
			if r.Invalid || out["error"] == nil || out["error"].(map[string]any)["code"] != tc.code {
				t.Errorf("%s: reply = %s", name, r.Body)
			}
			if strings.Contains(string(r.Body), "secret internal detail") {
				t.Errorf("%s leaked an internal error", name)
			}
		}
	})
	t.Run("bad JSON", func(t *testing.T) {
		if r, out := handle(t, `{nope`); !r.Invalid || out["error"].(map[string]any)["code"] != float64(mcp.CodeParseError) {
			t.Errorf("reply = %s", r.Body)
		}
	})
	t.Run("batch", func(t *testing.T) {
		r, _ := handle(t, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`)
		var out []map[string]any
		if err := json.Unmarshal(r.Body, &out); err != nil || len(out) != 2 {
			t.Errorf("batch reply = %s (%v)", r.Body, err)
		}
	})
}

// handlerOver serves a Server over HTTP the way the adapter does.
func handlerOver(s *mcp.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rep := s.Handle(r.Context(), body)
		if rep.Accepted {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rep.Body)
	})
}

func TestClientServerRoundTrip(t *testing.T) {
	srv := httptest.NewServer(handlerOver(mcp.NewServer(mcp.Implementation{Name: "engi"}, stubTools{})))
	defer srv.Close()
	c, err := mcp.Connect(context.Background(), srv.Client(), cfg(srv.URL, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if c.Server().ServerInfo.Name != "engi" {
		t.Errorf("server info = %+v", c.Server())
	}
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "add" {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	res, err := c.CallTool(context.Background(), "add", json.RawMessage(`{"x":2}`))
	if err != nil || res.Text() != `sum {"x":2}` {
		t.Errorf("call = %+v, %v", res, err)
	}
}
