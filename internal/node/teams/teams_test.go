package teams

import (
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

	"github.com/engi-hq/engi/internal/credtype/oauth2"
	"github.com/engi-hq/engi/internal/credtype/teamswebhook"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const (
	hookSecret  = "supersecret-webhook-path"
	graphSecret = "graph-access-token"
)

func creds(hookURL string) map[string]nodesdk.Credential {
	return map[string]nodesdk.Credential{
		credWebhook: {Type: teamswebhook.Name, Data: map[string]any{"url": hookURL}, Auth: teamswebhook.Type().Authenticate},
		credGraph:   {Type: oauth2.Name, Data: map[string]any{oauth2.FieldAccess: graphSecret}, Auth: &nodesdk.AuthSpec{Kind: nodesdk.AuthBearer, TokenField: oauth2.FieldAccess}},
	}
}

func exec(n *Node, client nodesdk.HTTPClient, hookURL string, params map[string]any) (nodesdk.Output, error) {
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params)
	in.Client = client
	in.Credentials = creds(hookURL)
	return n.Execute(context.Background(), in)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := New(Config{}).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWebhook(t *testing.T) {
	adaptive := map[string]any{"type": "AdaptiveCard", "version": "1.4"}
	message := map[string]any{"@type": "MessageCard", "title": "t"}
	tests := []struct {
		name   string
		params map[string]any
		check  func(t *testing.T, body map[string]any)
	}{
		{"text", map[string]any{"mode": "webhook", "text": "hello"}, func(t *testing.T, b map[string]any) {
			if b["@type"] != "MessageCard" || b["text"] != "hello" {
				t.Errorf("body %v", b)
			}
		}},
		{"message card", map[string]any{"mode": "webhook", "card": message}, func(t *testing.T, b map[string]any) {
			if b["title"] != "t" {
				t.Errorf("body %v", b)
			}
		}},
		{"adaptive card", map[string]any{"mode": "webhook", "card": adaptive}, func(t *testing.T, b map[string]any) {
			a := b["attachments"].([]any)[0].(map[string]any)
			if b["type"] != "message" || a["contentType"] != adaptiveContentType || a["content"].(map[string]any)["version"] != "1.4" {
				t.Errorf("body %v", b)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()
			out, err := exec(New(Config{}), srv.Client(), srv.URL+"/hooks/"+hookSecret, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if path != "/hooks/"+hookSecret || out.Ports[0][0].JSON["status"] != 202.0 {
				t.Errorf("path %q out %v", path, out.Ports[0][0].JSON)
			}
			tc.check(t, body)
		})
	}
}

func TestWebhookErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "Webhook failed for "+r.URL.Path)
	}))
	defer srv.Close()
	hook := srv.URL + "/" + hookSecret
	_, err := exec(New(Config{}), srv.Client(), hook, map[string]any{"mode": "webhook", "text": "x"})
	if !errors.Is(err, ErrTeams) || strings.Contains(err.Error(), hookSecret) {
		t.Fatalf("api error: %v", err)
	}
	if _, err := exec(New(Config{}), srv.Client(), hook, map[string]any{"mode": "webhook", "text": " "}); err == nil {
		t.Error("empty message must fail")
	}
	if _, err := exec(New(Config{}), srv.Client(), "not a url", map[string]any{"mode": "webhook", "text": "x"}); err == nil {
		t.Error("bad webhook URL must fail")
	}
	if _, err := exec(New(Config{}), srv.Client(), hook, map[string]any{"mode": "nope"}); err == nil {
		t.Error("unknown mode must fail")
	}
}

func TestGraph(t *testing.T) {
	var path, auth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.EscapedPath(), r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"m1"}`)
	}))
	defer srv.Close()
	out, err := exec(New(Config{GraphBaseURL: srv.URL + "/v1.0"}), srv.Client(), "", map[string]any{
		"mode": "graph", "teamId": "t-1", "channelId": "19:abc@thread.tacv2", "text": "<b>hi</b>", "contentType": "html"})
	if err != nil {
		t.Fatal(err)
	}
	b := body["body"].(map[string]any)
	if path != "/v1.0/teams/t-1/channels/19:abc@thread.tacv2/messages" && path != "/v1.0/teams/t-1/channels/19:abc%40thread.tacv2/messages" {
		t.Errorf("path %q", path)
	}
	if auth != "Bearer "+graphSecret || b["contentType"] != "html" || b["content"] != "<b>hi</b>" || out.Ports[0][0].JSON["id"] != "m1" {
		t.Errorf("auth=%q body=%v out=%v", auth, body, out.Ports[0][0].JSON)
	}
}

func TestGraphErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"Forbidden","message":"denied for `+graphSecret+`"}}`)
	}))
	defer srv.Close()
	n := New(Config{GraphBaseURL: srv.URL})
	_, err := exec(n, srv.Client(), "", map[string]any{"mode": "graph", "teamId": "t", "channelId": "c", "text": "x", "contentType": "text"})
	if !errors.Is(err, ErrTeams) || !strings.Contains(err.Error(), "Forbidden") || strings.Contains(err.Error(), graphSecret) {
		t.Fatalf("got %v", err)
	}
	for name, p := range map[string]map[string]any{
		"no team":    {"mode": "graph", "teamId": "", "channelId": "c", "text": "x", "contentType": "text"},
		"no channel": {"mode": "graph", "teamId": "t", "channelId": "", "text": "x", "contentType": "text"},
	} {
		if _, err := exec(n, srv.Client(), "", p); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

func TestRateLimit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	var slept []time.Duration
	n := New(Config{GraphBaseURL: srv.URL, Caller: chatkit.Caller{Limits: chatkit.Limits{MaxRetryWait: 10 * time.Second, MaxRetries: 1},
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }}})
	if _, err := exec(n, srv.Client(), "", map[string]any{"mode": "graph", "teamId": "t", "channelId": "c", "text": "x", "contentType": "text"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] != 4*time.Second {
		t.Errorf("calls=%d slept=%v", calls, slept)
	}
}

func TestSecretNeverInTransportErrors(t *testing.T) {
	n := New(Config{GraphBaseURL: "https://graph.test"})
	client := nodetest.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: errors.New("refused " + r.URL.Path)}
	})
	_, err := exec(n, client, "https://hooks.test/"+hookSecret, map[string]any{"mode": "webhook", "text": "x"})
	if err == nil || strings.Contains(err.Error(), hookSecret) {
		t.Fatalf("webhook: %v", err)
	}
}
