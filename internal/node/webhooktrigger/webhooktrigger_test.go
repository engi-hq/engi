package webhooktrigger

import (
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescribeIsValid(t *testing.T) {
	if err := New().Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookSpec(t *testing.T) {
	n := Node{}
	tests := []struct {
		name   string
		params map[string]any
		want   nodesdk.WebhookSpec
		err    string
	}{
		{"defaults", nil, nodesdk.WebhookSpec{Method: "GET", ResponseMode: "immediately", Auth: "none", ResponseData: "firstEntryJson"}, ""},
		{"full", map[string]any{
			"httpMethod": "post", "path": "/orders/:id/", "responseMode": "lastNode", "authentication": "basic",
			"responseCode": 201.0, "responseData": "allEntries", "rawBody": true,
		}, nodesdk.WebhookSpec{Method: "POST", Path: "orders/:id", ResponseMode: "lastNode", Auth: "basic", AuthCredential: "basicAuth",
			ResponseCode: 201, ResponseData: "allEntries", RawBody: true}, ""},
		{"header auth", map[string]any{"authentication": "header"}, nodesdk.WebhookSpec{Method: "GET", ResponseMode: "immediately",
			Auth: "header", AuthCredential: "headerAuth", ResponseData: "firstEntryJson"}, ""},
		{"bad method", map[string]any{"httpMethod": "TRACE"}, nodesdk.WebhookSpec{}, "httpMethod"},
		{"bad mode", map[string]any{"responseMode": "never"}, nodesdk.WebhookSpec{}, "responseMode"},
		{"bad auth", map[string]any{"authentication": "jwt"}, nodesdk.WebhookSpec{}, "authentication"},
		{"bad data", map[string]any{"responseData": "x"}, nodesdk.WebhookSpec{}, "responseData"},
		{"bad code", map[string]any{"responseCode": 42.0}, nodesdk.WebhookSpec{}, "responseCode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := n.Webhook(tt.params)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestExecuteForwardsTriggerItem(t *testing.T) {
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"body": map[string]any{"a": 1.0}}), nil)
	out := nodetest.Run(t, n, in)
	if len(out.Ports[0]) != 1 || out.Ports[0][0].JSON["body"] == nil {
		t.Fatalf("out = %+v", out)
	}
}
