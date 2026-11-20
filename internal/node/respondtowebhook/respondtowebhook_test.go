package respondtowebhook

import (
	"context"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func run(t *testing.T, params map[string]any, items ...map[string]any) (status int, ct, body string, hdr map[string][]string) {
	t.Helper()
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	out := nodetest.Run(t, n, in)
	if out.Response == nil {
		t.Fatal("no response")
	}
	if len(out.Ports[0]) != len(items) {
		t.Fatalf("items must pass through, got %d", len(out.Ports[0]))
	}
	return out.Response.Status, out.Response.Headers.Get("Content-Type"), string(out.Response.Body), out.Response.Headers
}

func TestResponses(t *testing.T) {
	a, b := map[string]any{"a": 1.0}, map[string]any{"b": "x"}
	tests := []struct {
		name       string
		params     map[string]any
		items      []map[string]any
		wantStatus int
		wantCT     string
		wantBody   string
	}{
		{"first item default", nil, []map[string]any{a, b}, 200, "application/json", `{"a":1}`},
		{"first item none", nil, nil, 200, "application/json", `{}`},
		{"all items", map[string]any{"respondWith": "allItems"}, []map[string]any{a, b}, 200, "application/json", `[{"a":1},{"b":"x"}]`},
		{"text", map[string]any{"respondWith": "text", "responseBody": "hello", "responseCode": 201.0}, []map[string]any{a}, 201, "text/plain; charset=utf-8", "hello"},
		{"json string", map[string]any{"respondWith": "json", "responseBody": `{"ok":true}`}, []map[string]any{a}, 200, "application/json", `{"ok":true}`},
		{"json object", map[string]any{"respondWith": "json", "responseBody": map[string]any{"ok": true}}, []map[string]any{a}, 200, "application/json", `{"ok":true}`},
		{"no data", map[string]any{"respondWith": "noData", "responseCode": 204.0}, []map[string]any{a}, 204, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, ct, body, _ := run(t, tt.params, tt.items...)
			if status != tt.wantStatus || ct != tt.wantCT || body != tt.wantBody {
				t.Fatalf("got %d %q %q; want %d %q %q", status, ct, body, tt.wantStatus, tt.wantCT, tt.wantBody)
			}
		})
	}
}

func TestHeadersAndContentTypeOverride(t *testing.T) {
	_, ct, _, h := run(t, map[string]any{
		"respondWith": "text", "responseBody": "x",
		"headers": []any{map[string]any{"name": "X-Trace", "value": "42"}, map[string]any{"name": "content-type", "value": "text/csv"}},
	}, map[string]any{})
	if h["X-Trace"][0] != "42" || ct != "text/csv" {
		t.Fatalf("headers = %v ct = %q", h, ct)
	}
}

func TestErrors(t *testing.T) {
	n := New()
	for name, tt := range map[string]struct {
		params map[string]any
		want   string
	}{
		"bad json":   {map[string]any{"respondWith": "json", "responseBody": "{nope"}, "valid JSON"},
		"bad code":   {map[string]any{"responseCode": 5.0}, "HTTP status"},
		"bad option": {map[string]any{"respondWith": "carrier pigeon"}, "unknown respondWith"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDescribeIsValid(t *testing.T) {
	if err := New().Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}
