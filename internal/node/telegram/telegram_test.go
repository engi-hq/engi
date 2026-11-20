package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	credtelegram "github.com/engi-hq/engi/internal/credtype/telegram"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const secretToken = "123456:SECRET-bot-token"

func credentials() map[string]nodesdk.Credential {
	return map[string]nodesdk.Credential{credtelegram.Name: {Type: credtelegram.Name, Data: map[string]any{"token": secretToken}, Auth: credtelegram.Type().Authenticate}}
}

func exec(n *Node, client nodesdk.HTTPClient, params map[string]any) (nodesdk.Output, error) {
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params)
	in.Client = client
	in.Credentials = credentials()
	return n.Execute(context.Background(), in)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := New(Config{}).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOperations(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		method string
		want   map[string]any
	}{
		{"send", map[string]any{"operation": "sendMessage", "chatId": "42", "text": "hi", "parseMode": "HTML", "disablePreview": true, "replyTo": 7.0},
			"sendMessage", map[string]any{"chat_id": "42", "text": "hi", "parse_mode": "HTML",
				"link_preview_options": map[string]any{"is_disabled": true}, "reply_parameters": map[string]any{"message_id": 7.0}}},
		{"send plain", map[string]any{"operation": "sendMessage", "chatId": "@chan", "text": "hi"},
			"sendMessage", map[string]any{"chat_id": "@chan", "text": "hi"}},
		{"edit", map[string]any{"operation": "editMessage", "chatId": "42", "messageId": 9.0, "text": "new"},
			"editMessageText", map[string]any{"chat_id": "42", "message_id": 9.0, "text": "new"}},
		{"document", map[string]any{"operation": "sendDocument", "chatId": "42", "documentUrl": "https://x.io/a.pdf", "caption": "c"},
			"sendDocument", map[string]any{"chat_id": "42", "document": "https://x.io/a.pdf", "caption": "c"}},
		{"get chat", map[string]any{"operation": "getChat", "chatId": "42"},
			"getChat", map[string]any{"chat_id": "42"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&got)
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":5}}`)
			}))
			defer srv.Close()
			out, err := exec(New(Config{BaseURL: srv.URL + "/prefix/"}), srv.Client(), tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if want := "/prefix/bot" + secretToken + "/" + tc.method; gotPath != want {
				t.Errorf("path %q, want %q", gotPath, want)
			}
			wantJSON, _ := json.Marshal(tc.want)
			gotJSON, _ := json.Marshal(got)
			if !bytes.Equal(wantJSON, gotJSON) {
				t.Errorf("body %s, want %s", gotJSON, wantJSON)
			}
			if out.Ports[0][0].JSON["message_id"] != 5.0 {
				t.Errorf("output %v", out.Ports[0][0].JSON)
			}
		})
	}
}

func TestBoolResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ok":true,"result":true}`) }))
	defer srv.Close()
	out, err := exec(New(Config{BaseURL: srv.URL}), srv.Client(), map[string]any{"operation": "editMessage", "chatId": "1", "messageId": 1.0, "text": "x"})
	if err != nil || out.Ports[0][0].JSON["result"] != true {
		t.Fatalf("%v %v", out, err)
	}
}

func TestValidationErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	for name, params := range map[string]map[string]any{
		"unknown op":  {"operation": "nope", "chatId": "1"},
		"no chat":     {"operation": "getChat", "chatId": ""},
		"no text":     {"operation": "sendMessage", "chatId": "1", "text": ""},
		"no doc":      {"operation": "sendDocument", "chatId": "1", "documentUrl": ""},
		"bad message": {"operation": "editMessage", "chatId": "1", "text": "x", "messageId": 0.0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec(New(Config{BaseURL: srv.URL}), srv.Client(), params); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if calls != 0 {
		t.Errorf("invalid input reached the API %d times", calls)
	}
}

func TestErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()
	_, err := exec(New(Config{BaseURL: srv.URL}), srv.Client(), map[string]any{"operation": "getChat", "chatId": "1"})
	if !errors.Is(err, ErrTelegram) || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("got %v", err)
	}
	var ne, inner *nodesdk.NodeError
	if !errors.As(err, &ne) || !errors.As(ne.Err, &inner) || inner.HTTPStatus != 400 {
		t.Errorf("want HTTP status 400 on %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		limits     chatkit.Limits
		wantErr    bool
		wantCalls  int
	}{
		{"waits then succeeds", "2", chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 1}, false, 2},
		{"wait over cap", "60", chatkit.Limits{MaxRetryWait: 5 * time.Second, MaxRetries: 1}, true, 1},
		{"no retries allowed", "1", chatkit.Limits{MaxRetryWait: 5 * time.Second}, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					w.Header().Set("Retry-After", tc.retryAfter)
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests"}`)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
			}))
			defer srv.Close()
			var slept []time.Duration
			n := New(Config{BaseURL: srv.URL, Caller: chatkit.Caller{Limits: tc.limits, Sleep: func(_ context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			}}})
			_, err := exec(n, srv.Client(), map[string]any{"operation": "getChat", "chatId": "1"})
			if (err != nil) != tc.wantErr || calls != tc.wantCalls {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if !tc.wantErr && (len(slept) != 1 || slept[0] != 2*time.Second) {
				t.Errorf("slept %v", slept)
			}
		})
	}
}

func TestSecretNeverInErrors(t *testing.T) {
	n := New(Config{BaseURL: "https://api.telegram.test"})
	for name, client := range map[string]nodesdk.HTTPClient{
		"transport": nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			// the real client's error embeds the full URL, which carries the token
			return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: errors.New("dial tcp: lookup failed")}
		}),
		"api": nodetest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"bad ` + secretToken + `"}`))}, nil
		}),
		"transport leaks token in text": nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("failed " + r.URL.Path)
		}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := exec(n, client, map[string]any{"operation": "getChat", "chatId": "1"})
			if err == nil || strings.Contains(err.Error(), secretToken) {
				t.Fatalf("error must exist and hide the token: %v", err)
			}
		})
	}
}

func TestAuthenticateNeedsToken(t *testing.T) {
	n := New(Config{BaseURL: "https://api.telegram.test"})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"operation": "getChat", "chatId": "1"})
	in.Client = nodetest.RoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not send"); return nil, nil })
	in.Credentials = map[string]nodesdk.Credential{credtelegram.Name: {Data: map[string]any{}, Auth: credtelegram.Type().Authenticate}}
	if _, err := n.Execute(context.Background(), in); err == nil {
		t.Fatal("want error")
	}
}
