package aitoolhttp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aitoolhttp"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const schema = `{"type":"object","properties":{"id":{"type":"string"},"q":{"type":"string"},"n":{"type":"number"}},"required":["id"]}`

type seen struct {
	method, url, body, ctype, header, auth string
}

func tool(t *testing.T, params map[string]any, reply func(*http.Request) *http.Response, got *seen) (port.Tool, error) {
	t.Helper()
	n := aitoolhttp.New(aitoolhttp.Config{MaxResponseBytes: 20})
	p := map[string]any{"parameters": schema}
	for k, v := range params {
		p[k] = v
	}
	in := nodetest.New(n.Describe(), nil, p)
	in.Client = nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		if got != nil {
			*got = seen{r.Method, r.URL.String(), string(b), r.Header.Get("Content-Type"), r.Header.Get("X-Id"), r.Header.Get("Authorization")}
		}
		return reply(r), nil
	})
	in.Credentials = map[string]nodesdk.Credential{"engi.http.bearer": {Type: "engi.http.bearer", Data: map[string]any{"token": "tkn"},
		Auth: &nodesdk.AuthSpec{Kind: nodesdk.AuthCustom, Custom: func(r *http.Request, d map[string]any) error {
			r.Header.Set("Authorization", "Bearer "+d["token"].(string))
			return nil
		}}}}
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		return nil, err
	}
	tools, _ := v.(port.ToolProvider).Tools(context.Background())
	return tools[0], nil
}

func resp(status int, body string) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
	}
}

func TestDescriptor(t *testing.T) {
	d := aitoolhttp.New(aitoolhttp.Config{}).Describe()
	if err := d.Validate(); err != nil || !d.IsSubNode() {
		t.Fatalf("invalid: %v", err)
	}
}

func TestGetFillsPathQueryAndHeaders(t *testing.T) {
	var got seen
	tl, err := tool(t, map[string]any{
		"url":     "https://api.test/users/{id}?x={q}",
		"query":   []any{map[string]any{"name": "page", "value": "{n}"}},
		"headers": []any{map[string]any{"name": "X-Id", "value": "id-{id}"}},
	}, resp(200, `{"ok":true}`), &got)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tl.Call(context.Background(), json.RawMessage(`{"id":"a/b c","q":"x&y","n":2}`))
	if err != nil || res.IsError || res.Content != `{"ok":true}` {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if got.method != "GET" || got.header != "id-a/b c" {
		t.Fatalf("got = %+v", got)
	}
	if got.url != "https://api.test/users/a%2Fb%20c?page=2&x=x%26y" && got.url != "https://api.test/users/a%2Fb%20c?x=x%26y&page=2" {
		t.Fatalf("url = %s", got.url)
	}
}

func TestJSONBodyKeepsTypes(t *testing.T) {
	var got seen
	tl, err := tool(t, map[string]any{"method": "POST", "url": "https://api.test/x", "bodyType": "json",
		"body": `{"count":"{n}","label":"item {id}","nested":{"id":"{id}"},"fixed":true}`}, resp(201, "created"), &got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tl.Call(context.Background(), json.RawMessage(`{"id":"7","n":3}`)); err != nil {
		t.Fatal(err)
	}
	if got.ctype != "application/json" {
		t.Fatalf("content type = %q", got.ctype)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(got.body), &b); err != nil {
		t.Fatal(err)
	}
	if b["count"] != float64(3) || b["label"] != "item 7" || b["nested"].(map[string]any)["id"] != "7" || b["fixed"] != true {
		t.Fatalf("body = %s", got.body)
	}
}

func TestNon2xxAndTruncation(t *testing.T) {
	tl, _ := tool(t, map[string]any{"url": "https://api.test/{id}"}, resp(404, "no such thing"), nil)
	res, err := tl.Call(context.Background(), json.RawMessage(`{"id":"1"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "404") || !strings.Contains(res.Content, "no such thing") {
		t.Fatalf("result = %+v, %v", res, err)
	}
	long, _ := tool(t, map[string]any{"url": "https://api.test/{id}"}, resp(200, strings.Repeat("a", 50)), nil)
	res, _ = long.Call(context.Background(), json.RawMessage(`{"id":"1"}`))
	if res.IsError || !strings.HasSuffix(res.Content, "[response truncated]") || !strings.HasPrefix(res.Content, strings.Repeat("a", 20)) {
		t.Fatalf("truncated = %+v", res)
	}
	res, _ = long.Call(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("missing required argument must be an error result")
	}
}

func TestAuthentication(t *testing.T) {
	var got seen
	tl, err := tool(t, map[string]any{"url": "https://api.test/{id}", "authentication": "generic", "genericAuthType": "engi.http.bearer"}, resp(200, "ok"), &got)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tl.Call(context.Background(), json.RawMessage(`{"id":"1"}`)); err != nil || got.auth != "Bearer tkn" {
		t.Fatalf("auth = %q, %v", got.auth, err)
	}
}

func TestSupplyRejectsUnsafeOrBrokenTemplates(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"placeholder in host":   {"url": "https://{id}.evil.test/x"},
		"placeholder as host":   {"url": "https://{id}"},
		"unknown placeholder":   {"url": "https://api.test/{nope}"},
		"unknown in body":       {"url": "https://api.test/", "method": "POST", "bodyType": "json", "body": `{"a":"{nope}"}`},
		"bad body json":         {"url": "https://api.test/", "method": "POST", "bodyType": "json", "body": `{`},
		"not http":              {"url": "ftp://x/y"},
		"missing url":           {},
		"unknown placeholder q": {"url": "https://api.test/", "query": []any{map[string]any{"name": "a", "value": "{zzz}"}}},
	} {
		if _, err := tool(t, params, resp(200, ""), nil); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestHeaderInjectionRejected(t *testing.T) {
	tl, err := tool(t, map[string]any{"url": "https://api.test/", "headers": []any{map[string]any{"name": "X-Id", "value": "{id}"}}}, resp(200, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := tl.Call(context.Background(), json.RawMessage(`{"id":"a\r\nX-Evil: 1"}`))
	if !res.IsError || !strings.Contains(res.Content, "line break") {
		t.Fatalf("result = %+v", res)
	}
}
