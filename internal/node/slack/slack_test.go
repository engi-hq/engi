package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	credslack "github.com/engi-hq/engi/internal/credtype/slack"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const secretToken = "xoxb-super-secret"

type recorded struct {
	path, query, auth, ctype string
	body                     map[string]any
	raw                      string
}

type fixture struct {
	mu   sync.Mutex
	reqs []recorded
	srv  *httptest.Server
}

func newFixture(t *testing.T, handler func(f *fixture, w http.ResponseWriter, r *http.Request)) *fixture {
	t.Helper()
	f := &fixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec := recorded{path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"), ctype: r.Header.Get("Content-Type"), raw: string(b)}
		_ = json.Unmarshal(b, &rec.body)
		f.mu.Lock()
		f.reqs = append(f.reqs, rec)
		f.mu.Unlock()
		handler(f, w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func okJSON(w http.ResponseWriter, v map[string]any) {
	v["ok"] = true
	_ = json.NewEncoder(w).Encode(v)
}

func run(t *testing.T, f *fixture, caller chatkit.Caller, params map[string]any) (nodesdk.Output, error) {
	t.Helper()
	n := New(Config{BaseURL: f.srv.URL, MaxListResults: 5, PageSize: 2, Caller: caller})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params)
	in.Client = f.srv.Client()
	ty := credslack.Type()
	in.Credentials = map[string]nodesdk.Credential{credslack.Name: {Type: credslack.Name, Data: map[string]any{"token": secretToken}, Auth: ty.Authenticate}}
	return n.Execute(context.Background(), in)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := New(Config{}).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOperations(t *testing.T) {
	handler := func(f *fixture, w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			okJSON(w, map[string]any{"upload_url": f.srv.URL + "/upload/F1", "file_id": "F1"})
		case "/upload/F1":
			w.WriteHeader(http.StatusOK)
		case "/conversations.list":
			if r.URL.Query().Get("cursor") == "" {
				okJSON(w, map[string]any{"channels": []any{"a", "b"}, "response_metadata": map[string]any{"next_cursor": "c2"}})
			} else {
				okJSON(w, map[string]any{"channels": []any{"c"}, "response_metadata": map[string]any{"next_cursor": ""}})
			}
		default:
			okJSON(w, map[string]any{"ts": "1.2"})
		}
	}
	tests := []struct {
		name   string
		params map[string]any
		path   string
		check  func(t *testing.T, reqs []recorded, out map[string]any)
	}{
		{"post", map[string]any{"operation": "postMessage", "channel": "C1", "text": "hi", "blocks": `[{"type":"divider"}]`, "threadTs": "9.9"},
			"/chat.postMessage", func(t *testing.T, r []recorded, _ map[string]any) {
				if r[0].body["channel"] != "C1" || r[0].body["thread_ts"] != "9.9" || len(r[0].body["blocks"].([]any)) != 1 {
					t.Errorf("body %v", r[0].body)
				}
				if r[0].auth != "Bearer "+secretToken || !strings.HasPrefix(r[0].ctype, "application/json") {
					t.Errorf("headers %q %q", r[0].auth, r[0].ctype)
				}
			}},
		{"update", map[string]any{"operation": "updateMessage", "channel": "C1", "ts": "1.2", "text": "new", "blocks": []any{map[string]any{"type": "divider"}}},
			"/chat.update", func(t *testing.T, r []recorded, _ map[string]any) {
				if r[0].body["ts"] != "1.2" || r[0].body["text"] != "new" {
					t.Errorf("body %v", r[0].body)
				}
			}},
		{"delete", map[string]any{"operation": "deleteMessage", "channel": "C1", "ts": "1.2"},
			"/chat.delete", func(t *testing.T, r []recorded, _ map[string]any) {
				if r[0].body["ts"] != "1.2" {
					t.Errorf("body %v", r[0].body)
				}
			}},
		{"user", map[string]any{"operation": "getUserByEmail", "email": "a+b@x.io"},
			"/users.lookupByEmail", func(t *testing.T, r []recorded, _ map[string]any) {
				if v, _ := url.ParseQuery(r[0].query); v.Get("email") != "a+b@x.io" {
					t.Errorf("query %q", r[0].query)
				}
			}},
		{"upload", map[string]any{"operation": "uploadFile", "channel": "C1", "content": "hello", "filename": "a.txt", "title": "T", "comment": "cm"},
			"/files.completeUploadExternal", func(t *testing.T, r []recorded, _ map[string]any) {
				if len(r) != 3 || r[0].path != "/files.getUploadURLExternal" || r[1].raw != "hello" || r[1].auth != "" {
					t.Fatalf("unexpected flow %+v", r)
				}
				if v, _ := url.ParseQuery(r[0].query); v.Get("length") != "5" || v.Get("filename") != "a.txt" {
					t.Errorf("query %q", r[0].query)
				}
				if r[2].body["channel_id"] != "C1" || r[2].body["initial_comment"] != "cm" {
					t.Errorf("complete body %v", r[2].body)
				}
			}},
		{"channels paginated and capped", map[string]any{"operation": "listChannels", "types": "public_channel", "excludeArchived": true},
			"/conversations.list", func(t *testing.T, r []recorded, out map[string]any) {
				if len(r) != 2 || out["count"] != 3.0 || out["truncated"] != false {
					t.Errorf("reqs=%d out=%v", len(r), out)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, handler)
			out, err := run(t, f, chatkit.Caller{}, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.reqs[len(f.reqs)-1].path; got != tc.path {
				t.Fatalf("last path %q, want %q", got, tc.path)
			}
			tc.check(t, f.reqs, out.Ports[0][0].JSON)
		})
	}
}

func TestListChannelsCap(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		okJSON(w, map[string]any{"channels": []any{1, 2, 3}, "response_metadata": map[string]any{"next_cursor": "more"}})
	})
	out, err := run(t, f, chatkit.Caller{}, map[string]any{"operation": "listChannels", "excludeArchived": false})
	if err != nil {
		t.Fatal(err)
	}
	j := out.Ports[0][0].JSON
	if j["count"] != 5.0 || j["truncated"] != true || len(f.reqs) != 2 {
		t.Errorf("out=%v reqs=%d", j, len(f.reqs))
	}
}

func TestValidationErrors(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) { okJSON(w, map[string]any{}) })
	for name, params := range map[string]map[string]any{
		"unknown op":       {"operation": "nope"},
		"post no channel":  {"operation": "postMessage", "channel": "", "text": "x"},
		"post no content":  {"operation": "postMessage", "channel": "C"},
		"bad blocks":       {"operation": "postMessage", "channel": "C", "blocks": "{"},
		"delete no ts":     {"operation": "deleteMessage", "channel": "C", "ts": ""},
		"user no email":    {"operation": "getUserByEmail", "email": ""},
		"update no ts":     {"operation": "updateMessage", "channel": "C", "text": "x", "ts": ""},
		"upload no filenm": {"operation": "uploadFile", "channel": "C", "filename": ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(t, f, chatkit.Caller{}, params); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if len(f.reqs) != 0 {
		t.Errorf("invalid input must not reach the API, got %d requests", len(f.reqs))
	}
}

func TestErrorEnvelope(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
	})
	_, err := run(t, f, chatkit.Caller{}, map[string]any{"operation": "postMessage", "channel": "C1", "text": "x"})
	var ne *nodesdk.NodeError
	if !errors.Is(err, ErrSlack) || !errors.As(err, &ne) || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("got %v", err)
	}
}

func TestNonJSONResponse(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>bad gateway</html>")
	})
	_, err := run(t, f, chatkit.Caller{}, map[string]any{"operation": "postMessage", "channel": "C1", "text": "x"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("got %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	newHandler := func(limited int) func(*fixture, http.ResponseWriter, *http.Request) {
		return func(f *fixture, w http.ResponseWriter, _ *http.Request) {
			if len(f.reqs) <= limited {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			okJSON(w, map[string]any{"ts": "1"})
		}
	}
	params := map[string]any{"operation": "postMessage", "channel": "C1", "text": "x"}
	tests := []struct {
		name      string
		limited   int
		caller    chatkit.Limits
		wantErr   bool
		wantSleep int
	}{
		{"retry then success", 2, chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 2}, false, 2},
		{"retries exhausted", 3, chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 2}, true, 2},
		{"wait above cap", 1, chatkit.Limits{MaxRetryWait: time.Second, MaxRetries: 2}, true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, newHandler(tc.limited))
			var slept []time.Duration
			c := chatkit.Caller{Limits: tc.caller, Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
			_, err := run(t, f, c, params)
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, chatkit.ErrRateLimited)) {
				t.Fatalf("err = %v", err)
			}
			if len(slept) != tc.wantSleep || (len(slept) > 0 && slept[0] != 3*time.Second) {
				t.Errorf("slept %v", slept)
			}
		})
	}
}

func TestRateLimitWaitHonoursContext(t *testing.T) {
	f := newFixture(t, func(_ *fixture, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	n := New(Config{BaseURL: f.srv.URL, Caller: chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: time.Minute, MaxRetries: 1}}})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"operation": "postMessage", "channel": "C", "text": "x"})
	in.Client = f.srv.Client()
	in.Credentials = map[string]nodesdk.Credential{credslack.Name: {Data: map[string]any{"token": secretToken}, Auth: credslack.Type().Authenticate}}
	start := time.Now()
	if _, err := n.Execute(ctx, in); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

func TestSecretNeverInErrors(t *testing.T) {
	// Transport failure whose error text embeds the URL, plus an API error echoing the token.
	n := New(Config{BaseURL: "https://slack.test/api"})
	for name, client := range map[string]nodesdk.HTTPClient{
		"transport": nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Post", URL: r.URL.String() + "?t=" + secretToken, Err: errors.New("connection refused " + secretToken)}
		}),
		"api": nodetest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":"bad ` + secretToken + `"}`))}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"operation": "postMessage", "channel": "C", "text": "x"})
			in.Client = client
			in.Credentials = map[string]nodesdk.Credential{credslack.Name: {Data: map[string]any{"token": secretToken}, Auth: credslack.Type().Authenticate}}
			_, err := n.Execute(context.Background(), in)
			if err == nil || strings.Contains(err.Error(), secretToken) {
				t.Fatalf("error must exist and hide the token: %v", err)
			}
		})
	}
}
