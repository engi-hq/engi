package httprequest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// recorded is what the test server saw.
type recorded struct {
	method, path, query, contentType, body string
	header                                 http.Header
}

func server(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]recorded) {
	t.Helper()
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), string(body), r.Header.Clone()})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func jsonReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

func input(srv *httptest.Server, items int, params map[string]any) *nodetest.Input {
	n := New()
	objs := make([]map[string]any, items)
	for i := range objs {
		objs[i] = map[string]any{"i": float64(i)}
	}
	in := nodetest.New(n.Describe(), nodetest.Items(objs...), params)
	if srv != nil {
		if params["url"] == nil {
			params["url"] = srv.URL + "/x"
		}
		in.Client = srv.Client()
	}
	return in
}

func TestResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		params  map[string]any
		want    []map[string]any
	}{
		{"json object", jsonReply(`{"a":1,"b":{"c":[1,2]}}`), map[string]any{},
			[]map[string]any{{"a": 1.0, "b": map[string]any{"c": []any{1.0, 2.0}}}}},
		{"json array splits into items", jsonReply(`[{"id":1},{"id":2},3]`), map[string]any{},
			[]map[string]any{{"id": 1.0}, {"id": 2.0}, {"data": 3.0}}},
		{"empty json array yields no items", jsonReply(`[]`), map[string]any{}, []map[string]any{}},
		{"text body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "hello")
		},
			map[string]any{}, []map[string]any{{"data": "hello"}}},
		{"json content type with invalid json falls back to text", jsonReply(`not json`), map[string]any{},
			[]map[string]any{{"data": "not json"}}},
		{"forced json", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"k":"v"}`) },
			map[string]any{"responseFormat": "json"}, []map[string]any{{"k": "v"}}},
		{"forced text", jsonReply(`{"k":"v"}`), map[string]any{"responseFormat": "text"}, []map[string]any{{"data": `{"k":"v"}`}}},
		{"base64", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte{0, 1, 2, 255}) },
			map[string]any{"responseFormat": "base64"}, []map[string]any{{"data": "AAEC/w=="}}},
		{"empty body", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
			map[string]any{}, []map[string]any{{"data": ""}}},
		{"empty body forced json", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
			map[string]any{"responseFormat": "json"}, []map[string]any{{"data": nil}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := server(t, tt.handler)
			out := nodetest.Run(t, New(), input(srv, 1, tt.params))
			got := nodetest.JSONs(out.Ports[0])
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestFullResponse(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Thing", "a")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	out := nodetest.Run(t, New(), input(srv, 1, map[string]any{"fullResponse": true}))
	got := out.Ports[0][0].JSON
	if got["statusCode"] != 201.0 || got["statusMessage"] != "Created" || got["body"].(map[string]any)["ok"] != true {
		t.Fatalf("got %v", got)
	}
	if got["headers"].(map[string]any)["x-thing"] != "a" {
		t.Fatalf("headers = %v", got["headers"])
	}
}

func TestRequestShape(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		check  func(t *testing.T, r recorded)
	}{
		{"method and query", map[string]any{"method": "delete", "query": []any{map[string]any{"name": "a", "value": "1"}, map[string]any{"name": "a", "value": 2.0}, map[string]any{"name": "b c", "value": "&"}}},
			func(t *testing.T, r recorded) {
				if r.method != "DELETE" || r.query != "a=1&a=2&b+c=%26" {
					t.Fatalf("%+v", r)
				}
			}},
		{"query as object", map[string]any{"query": map[string]any{"z": "1", "a": true}},
			func(t *testing.T, r recorded) {
				if r.query != "a=true&z=1" {
					t.Fatalf("%+v", r)
				}
			}},
		{"url with existing query is extended", map[string]any{"url": "SRV/x?keep=1", "query": []any{map[string]any{"name": "a", "value": "2"}}},
			func(t *testing.T, r recorded) {
				if r.query != "a=2&keep=1" {
					t.Fatalf("%+v", r)
				}
			}},
		{"headers", map[string]any{"headers": []any{map[string]any{"name": "X-Token", "value": "abc"}, map[string]any{"name": "User-Agent", "value": "mine"}}},
			func(t *testing.T, r recorded) {
				if r.header.Get("X-Token") != "abc" || r.header.Get("User-Agent") != "mine" {
					t.Fatalf("%+v", r.header)
				}
			}},
		{"json body from object", map[string]any{"method": "POST", "bodyType": "json", "jsonBody": map[string]any{"a": 1.0, "b": []any{"x"}}},
			func(t *testing.T, r recorded) {
				if r.contentType != "application/json" || r.body != `{"a":1,"b":["x"]}` {
					t.Fatalf("%+v", r)
				}
			}},
		{"json body from text", map[string]any{"method": "PUT", "bodyType": "json", "jsonBody": `{"a": 1}`},
			func(t *testing.T, r recorded) {
				if r.method != "PUT" || r.body != `{"a": 1}` {
					t.Fatalf("%+v", r)
				}
			}},
		{"form body", map[string]any{"method": "POST", "bodyType": "form", "formFields": []any{map[string]any{"name": "a", "value": "x y"}, map[string]any{"name": "n", "value": 3.0}}},
			func(t *testing.T, r recorded) {
				if r.contentType != "application/x-www-form-urlencoded" || r.body != "a=x+y&n=3" {
					t.Fatalf("%+v", r)
				}
			}},
		{"raw body", map[string]any{"method": "PATCH", "bodyType": "raw", "rawBody": "<x/>", "rawContentType": "application/xml"},
			func(t *testing.T, r recorded) {
				if r.contentType != "application/xml" || r.body != "<x/>" {
					t.Fatalf("%+v", r)
				}
			}},
		{"explicit content type header wins over the body type", map[string]any{"method": "POST", "bodyType": "json", "jsonBody": map[string]any{}, "headers": []any{map[string]any{"name": "Content-Type", "value": "application/vnd.x+json"}}},
			func(t *testing.T, r recorded) {
				if r.contentType != "application/vnd.x+json" {
					t.Fatalf("%+v", r)
				}
			}},
		{"no body by default", map[string]any{"method": "POST"},
			func(t *testing.T, r recorded) {
				if r.body != "" || r.contentType != "" {
					t.Fatalf("%+v", r)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := server(t, jsonReply(`{}`))
			if u, ok := tt.params["url"].(string); ok {
				tt.params["url"] = strings.Replace(u, "SRV", srv.URL, 1)
			}
			nodetest.Run(t, New(), input(srv, 1, tt.params))
			if len(*seen) != 1 {
				t.Fatalf("requests = %d", len(*seen))
			}
			tt.check(t, (*seen)[0])
		})
	}
}

func TestOneRequestPerItemWithPerItemParams(t *testing.T) {
	srv, seen := server(t, jsonReply(`{"ok":true}`))
	in := input(srv, 3, map[string]any{})
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "url" {
			return srv.URL + "/item/" + string(rune('0'+i)), true
		}
		return nil, false
	}
	out := nodetest.Run(t, New(), in)
	if len(out.Ports[0]) != 3 || len(*seen) != 3 || (*seen)[2].path != "/item/2" {
		t.Fatalf("items=%d requests=%+v", len(out.Ports[0]), *seen)
	}
	if out.Ports[0][2].PairedItem[0].Item != 2 {
		t.Fatalf("pairing = %v", out.Ports[0][2].PairedItem)
	}
}

func TestEmptyInput(t *testing.T) {
	srv, seen := server(t, jsonReply(`{}`))
	out := nodetest.Run(t, New(), input(srv, 0, map[string]any{}))
	if len(out.Ports[0]) != 0 || len(*seen) != 0 {
		t.Fatal("no input items means no requests")
	}
}

func TestFailOnError(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"missing"}`)
	}
	srv, _ := server(t, handler)

	_, err := New().Execute(context.Background(), input(srv, 1, map[string]any{}))
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || ne.HTTPStatus != 404 || !strings.Contains(ne.Message, "404") || !strings.Contains(ne.Description, "missing") {
		t.Fatalf("err = %#v", err)
	}
	if ne.ItemIndex == nil || *ne.ItemIndex != 0 {
		t.Fatalf("item index = %v", ne.ItemIndex)
	}

	out := nodetest.Run(t, New(), input(srv, 1, map[string]any{"failOnError": false, "fullResponse": true}))
	if out.Ports[0][0].JSON["statusCode"] != 404.0 {
		t.Fatalf("got %v", out.Ports[0][0].JSON)
	}
}

func TestLongErrorBodyIsTruncated(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("x", 5000))
	})
	_, err := New().Execute(context.Background(), input(srv, 1, map[string]any{}))
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || len(ne.Description) > maxErrorBody+3 {
		t.Fatalf("description length = %d", len(ne.Description))
	}
}

func TestRedirects(t *testing.T) {
	var noFollow bool
	client := nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		noFollow = nodesdk.HTTPCallOptionsFrom(r.Context()).NoRedirect
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	for _, follow := range []bool{true, false} {
		in := input(nil, 1, map[string]any{"url": "http://example.test/", "followRedirects": follow})
		in.Client = client
		nodetest.Run(t, New(), in)
		if noFollow == follow {
			t.Fatalf("followRedirects=%v but NoRedirect=%v", follow, noFollow)
		}
	}
}

func TestTimeout(t *testing.T) {
	srv, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	start := time.Now()
	_, err := New().Execute(context.Background(), input(srv, 1, map[string]any{"timeoutMs": 50.0}))
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestTransportErrorAndUnavailableClient(t *testing.T) {
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"url": "http://example.test/"})
	_, err := n.Execute(context.Background(), in)
	if !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	in.Client = nodetest.RoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	if _, err := n.Execute(context.Background(), in); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
}

func TestBodyReadError(t *testing.T) {
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"url": "http://example.test/"})
	in.Client = nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(errReader{}), Request: r}, nil
	})
	if _, err := n.Execute(context.Background(), in); err == nil || !strings.Contains(err.Error(), "reading the response") {
		t.Fatalf("err = %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("too large") }

func TestInvalidSettings(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"missing url", map[string]any{"url": ""}, "url is required"},
		{"bad scheme", map[string]any{"url": "ftp://example.test/x"}, "http or https"},
		{"no scheme", map[string]any{"url": "example.test/x"}, "http or https"},
		{"no host", map[string]any{"url": "http:///x"}, "no host"},
		{"unparsable url", map[string]any{"url": "http://[::1"}, "invalid url"},
		{"bad json text", map[string]any{"method": "POST", "bodyType": "json", "jsonBody": "{nope"}, "not valid JSON"},
		{"unknown body type", map[string]any{"bodyType": "xml"}, "unknown body type"},
		{"unknown response format", map[string]any{"responseFormat": "yaml"}, "unknown response format"},
		{"bad header list", map[string]any{"headers": "x"}, "headers"},
		{"negative timeout", map[string]any{"timeoutMs": -1.0}, "negative"},
		{"form fields wrong type", map[string]any{"bodyType": "form", "formFields": 3.0}, "formFields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := server(t, jsonReply(`{}`))
			in := input(srv, 1, tt.params)
			if _, ok := tt.params["url"]; ok {
				in.Params["url"] = tt.params["url"]
			}
			_, err := New().Execute(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
			if len(*seen) != 0 {
				t.Fatal("no request may be sent for invalid settings")
			}
		})
	}
}

func TestDescriptor(t *testing.T) {
	if err := New().Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}
