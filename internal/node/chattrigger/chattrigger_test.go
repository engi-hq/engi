package chattrigger

import (
	"slices"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescribeIsValid(t *testing.T) {
	d := New().Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Type != "engi.trigger.chat" || d.Group != nodesdk.GroupTrigger {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestChatSpec(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		check  func(*testing.T, nodesdk.ChatSpec)
		err    string
	}{
		{"defaults", nil, func(t *testing.T, s nodesdk.ChatSpec) {
			if s.ResponseMode != "lastNode" || s.Auth != "none" || s.ReplyField != "output" || s.Title != "Chat" || s.Path != "" || len(s.AllowedOrigins) != 0 {
				t.Fatalf("spec = %+v", s)
			}
		}, ""},
		{"full", map[string]any{
			"path": "/support/", "responseMode": "responseNode", "authentication": "header", "replyField": "answer",
			"allowedOrigins": " https://a.example/ , https://b.example,,", "title": "Help", "welcomeMessage": "Hi", "inputPlaceholder": "Ask",
		}, func(t *testing.T, s nodesdk.ChatSpec) {
			if s.Path != "support" || s.ResponseMode != "responseNode" || s.AuthCredential != "headerAuth" || s.ReplyField != "answer" ||
				!slices.Equal(s.AllowedOrigins, []string{"https://a.example", "https://b.example"}) || s.Welcome != "Hi" {
				t.Fatalf("spec = %+v", s)
			}
		}, ""},
		{"basic", map[string]any{"authentication": "basic"}, func(t *testing.T, s nodesdk.ChatSpec) {
			if s.AuthCredential != "basicAuth" {
				t.Fatalf("spec = %+v", s)
			}
		}, ""},
		{"immediate is not offered", map[string]any{"responseMode": "immediately"}, nil, "responseMode"},
		{"bad auth", map[string]any{"authentication": "jwt"}, nil, "authentication"},
		{"params refused", map[string]any{"path": "a/:b"}, nil, "parameters"},
		{"reserved widget", map[string]any{"path": "a/widget"}, nil, "served by the host"},
		{"reserved asset", map[string]any{"path": "loader.js"}, nil, "served by the host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Node{}.Chat(tt.params)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, got)
		})
	}
}

func TestWebhookRegistersUnderTheChatMethod(t *testing.T) {
	spec, err := Node{}.Webhook(map[string]any{"path": "support", "authentication": "basic"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Method != nodesdk.WebhookMethodChat || spec.Path != "support" || spec.AuthCredential != "basicAuth" {
		t.Fatalf("spec = %+v", spec)
	}
	if _, err := (Node{}).Webhook(map[string]any{"responseMode": "x"}); err == nil {
		t.Fatal("want an error")
	}
}

func TestExecuteForwardsTriggerItem(t *testing.T) {
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"sessionId": "s1", "chatInput": "hi"}), nil)
	out := nodetest.Run(t, n, in)
	if len(out.Ports[0]) != 1 || out.Ports[0][0].JSON["chatInput"] != "hi" {
		t.Fatalf("out = %+v", out)
	}
}
