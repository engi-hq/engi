package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/llm/anthropic"
	"github.com/engi-hq/engi/internal/port"
)

type captured struct {
	header http.Header
	path   string
	body   map[string]any
}

func newClient(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*anthropic.Client, *captured) {
	t.Helper()
	rec := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.header, rec.path = r.Header.Clone(), r.URL.Path
		rec.body = nil
		_ = json.Unmarshal(b, &rec.body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := anthropic.New(anthropic.Config{APIKey: "sk-test", BaseURL: srv.URL + "/", Version: "2023-06-01", DefaultMaxTokens: 1000}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

const textReply = `{"model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"usage":{"input_tokens":10,"output_tokens":3}}`

func TestChatText(t *testing.T) {
	c, rec := newClient(t, reply(textReply))
	temp := 0.2
	resp, err := c.Chat(context.Background(), port.LLMRequest{
		Model: "claude-sonnet-5-5", System: "be brief", Temperature: &temp, Stop: []string{"END"},
		Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello world" || resp.StopReason != port.StopEnd || resp.Usage != (port.LLMUsage{InputTokens: 10, OutputTokens: 3}) || resp.Model != "claude-sonnet-5-5" {
		t.Fatalf("resp = %+v", resp)
	}
	if rec.path != "/v1/messages" || rec.header.Get("x-api-key") != "sk-test" || rec.header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("path %q headers %v", rec.path, rec.header)
	}
	if rec.body["max_tokens"] != float64(1000) || rec.body["system"] != "be brief" || rec.body["temperature"] != 0.2 {
		t.Fatalf("body = %v", rec.body)
	}
	if _, ok := rec.body["tools"]; ok {
		t.Fatal("tools sent without definitions")
	}
}

func TestStopReasons(t *testing.T) {
	for reason, want := range map[string]port.StopReason{
		"end_turn": port.StopEnd, "max_tokens": port.StopMaxTokens, "tool_use": port.StopToolUse,
		"stop_sequence": port.StopStopSequence, "refusal": port.StopOther,
	} {
		c, _ := newClient(t, reply(`{"stop_reason":"`+reason+`","content":[],"usage":{}}`))
		resp, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
		if err != nil || resp.StopReason != want {
			t.Errorf("%s: got %q, %v; want %q", reason, resp.StopReason, err, want)
		}
	}
}

func TestToolCallsRoundTrip(t *testing.T) {
	c, rec := newClient(t, reply(`{"stop_reason":"tool_use","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Oslo"}}],"usage":{"input_tokens":5,"output_tokens":7}}`))
	resp, err := c.Chat(context.Background(), port.LLMRequest{
		Model:      "m",
		Tools:      []port.ToolDefinition{{Name: "weather", Description: "get weather", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "noargs"}},
		ToolChoice: port.ToolChoice{Mode: port.ToolChoiceTool, Name: "weather"},
		Messages: []port.LLMMessage{
			{Role: port.RoleUser, Content: "weather?"},
			{Role: port.RoleAssistant, ToolCalls: []port.LLMToolCall{{ID: "a", Name: "weather", Arguments: json.RawMessage(`{"city":"Rome"}`)}, {ID: "b", Name: "noargs"}}},
			{Role: port.RoleTool, ToolCallID: "a", Content: "sunny"},
			{Role: port.RoleTool, ToolCallID: "b", Content: "boom", IsError: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != port.StopToolUse || resp.Content != "checking" || len(resp.ToolCalls) != 1 ||
		resp.ToolCalls[0].ID != "toolu_1" || string(resp.ToolCalls[0].Arguments) != `{"city":"Oslo"}` {
		t.Fatalf("resp = %+v", resp)
	}
	msgs := rec.body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v (tool results must merge into one user message)", msgs)
	}
	results := msgs[2].(map[string]any)["content"].([]any)
	if len(results) != 2 || results[1].(map[string]any)["is_error"] != true || results[0].(map[string]any)["tool_use_id"] != "a" {
		t.Fatalf("tool results = %v", results)
	}
	uses := msgs[1].(map[string]any)["content"].([]any)
	if uses[1].(map[string]any)["input"].(map[string]any) == nil {
		t.Fatalf("empty arguments must become {}: %v", uses)
	}
	tools := rec.body["tools"].([]any)
	if tools[1].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tool without schema must get one: %v", tools)
	}
	if tc := rec.body["tool_choice"].(map[string]any); tc["type"] != "tool" || tc["name"] != "weather" {
		t.Fatalf("tool_choice = %v", tc)
	}
}

func TestToolChoiceModes(t *testing.T) {
	for mode, want := range map[port.ToolChoiceMode]string{port.ToolChoiceAuto: "auto", port.ToolChoiceNone: "none", port.ToolChoiceRequired: "any"} {
		c, rec := newClient(t, reply(textReply))
		_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Tools: []port.ToolDefinition{{Name: "t"}}, ToolChoice: port.ToolChoice{Mode: mode}})
		if err != nil {
			t.Fatal(err)
		}
		if got := rec.body["tool_choice"].(map[string]any)["type"]; got != want {
			t.Errorf("%s: tool_choice type %v, want %s", mode, got, want)
		}
	}
}

func TestResponseFormats(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}}}`)
	t.Run("json adds a system instruction", func(t *testing.T) {
		c, rec := newClient(t, reply(textReply))
		if _, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", System: "s", Format: port.ResponseFormat{Kind: port.ResponseJSON}}); err != nil {
			t.Fatal(err)
		}
		if sys := rec.body["system"].(string); !strings.HasPrefix(sys, "s\n\n") || !strings.Contains(sys, "JSON") {
			t.Fatalf("system = %q", sys)
		}
	})
	t.Run("jsonSchema forces a tool and returns its arguments as text", func(t *testing.T) {
		c, rec := newClient(t, reply(`{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"answer","input":{"n":4}}],"usage":{"input_tokens":1,"output_tokens":2}}`))
		resp, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema, Name: "answer", Schema: schema}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Content != `{"n":4}` || len(resp.ToolCalls) != 0 || resp.StopReason != port.StopEnd {
			t.Fatalf("resp = %+v", resp)
		}
		tc := rec.body["tool_choice"].(map[string]any)
		tool := rec.body["tools"].([]any)[0].(map[string]any)
		if tc["type"] != "tool" || tc["name"] != "answer" || tool["name"] != "answer" || tool["input_schema"] == nil {
			t.Fatalf("body = %v", rec.body)
		}
	})
	t.Run("jsonSchema with tools falls back to a system instruction", func(t *testing.T) {
		c, rec := newClient(t, reply(textReply))
		_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Tools: []port.ToolDefinition{{Name: "t"}}, Format: port.ResponseFormat{Kind: port.ResponseJSONSchema, Schema: schema}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rec.body["system"].(string), `"properties"`) || len(rec.body["tools"].([]any)) != 1 {
			t.Fatalf("body = %v", rec.body)
		}
	})
	t.Run("jsonSchema without schema is rejected", func(t *testing.T) {
		c, _ := newClient(t, reply(textReply))
		if _, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema}}); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		header    string
		body      string
		retryable bool
		after     time.Duration
		message   string
	}{
		{"rate limited", 429, "7", `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, true, 7 * time.Second, "slow down"},
		{"overloaded", 529, "", `{"error":{"message":"overloaded"}}`, true, 0, "overloaded"},
		{"server error", 500, "", `oops`, true, 0, "oops"},
		{"bad request", 400, "", `{"error":{"message":"max_tokens too large"}}`, false, 0, "max_tokens too large"},
		{"unauthorized", 401, "", `{"error":{"message":"invalid x-api-key"}}`, false, 0, "invalid x-api-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
			var le *port.LLMError
			if !errors.As(err, &le) {
				t.Fatalf("err = %v", err)
			}
			if le.Status != tt.status || le.Retryable != tt.retryable || le.RetryAfter != tt.after || le.Message != tt.message || le.Provider != port.LLMProviderAnthropic {
				t.Fatalf("err = %+v", le)
			}
			if port.IsRetryableLLMError(err) != tt.retryable {
				t.Fatal("IsRetryableLLMError disagrees")
			}
			if strings.Contains(err.Error(), "sk-test") {
				t.Fatal("error leaks the API key")
			}
		})
	}
}

func TestTransportErrorIsRetryableAndContextIsNot(t *testing.T) {
	c, _ := newClient(t, reply(textReply))
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	dead, _ := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: url, Version: "v", DefaultMaxTokens: 1}, http.DefaultClient)
	if _, err := dead.Chat(context.Background(), port.LLMRequest{Model: "m"}); !port.IsRetryableLLMError(err) {
		t.Fatalf("connection refused should be retryable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Chat(ctx, port.LLMRequest{Model: "m"}); !errors.Is(err, context.Canceled) || port.IsRetryableLLMError(err) {
		t.Fatalf("cancelled ctx: %v", err)
	}
}

func TestNewValidates(t *testing.T) {
	for name, cfg := range map[string]anthropic.Config{
		"no key": {BaseURL: "u", Version: "v", DefaultMaxTokens: 1}, "no base": {APIKey: "k", Version: "v", DefaultMaxTokens: 1},
		"no max": {APIKey: "k", BaseURL: "u", Version: "v"},
	} {
		if _, err := anthropic.New(cfg, http.DefaultClient); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	c, _ := newClient(t, reply(textReply))
	if _, err := c.Chat(context.Background(), port.LLMRequest{}); err == nil {
		t.Error("empty model must fail")
	}
}

const sseText = `event: message_start
data: {"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":12,"output_tokens":1}}}

event: ping
data: {"type":"ping"}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"calc","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`

func sse(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}
}

func TestStreamTextAndToolCalls(t *testing.T) {
	c, rec := newClient(t, sse(sseText))
	var deltas []string
	resp, err := c.Stream(context.Background(), port.LLMRequest{Model: "m", Tools: []port.ToolDefinition{{Name: "calc"}}}, func(e port.StreamEvent) error {
		deltas = append(deltas, e.TextDelta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["stream"] != true {
		t.Fatal("stream flag not sent")
	}
	if strings.Join(deltas, "|") != "Hel|lo" || resp.Content != "Hello" {
		t.Fatalf("deltas %v content %q", deltas, resp.Content)
	}
	if resp.StopReason != port.StopToolUse || resp.Model != "claude-sonnet-5-5" || resp.Usage != (port.LLMUsage{InputTokens: 12, OutputTokens: 20}) {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_9" || string(resp.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
}

func TestStreamForcedSchemaToolStreamsAsText(t *testing.T) {
	body := `data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":1}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"structured_output"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"ok\":"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"true}"}}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}

data: {"type":"message_stop"}

`
	c, _ := newClient(t, sse(body))
	resp, err := c.Stream(context.Background(), port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema, Schema: json.RawMessage(`{"type":"object"}`)}}, func(port.StreamEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != `{"ok":true}` || len(resp.ToolCalls) != 0 || resp.StopReason != port.StopEnd {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestStreamFailures(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) })
		_, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return nil })
		if !port.IsRetryableLLMError(err) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("error event", func(t *testing.T) {
		c, _ := newClient(t, sse("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n"))
		_, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return nil })
		if !port.IsRetryableLLMError(err) || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("truncated stream", func(t *testing.T) {
		c, _ := newClient(t, sse("data: {\"type\":\"message_start\",\"message\":{}}\n\n"))
		if _, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return nil }); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("emit error aborts", func(t *testing.T) {
		c, _ := newClient(t, sse(sseText))
		stop := errors.New("stop")
		if _, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return stop }); !errors.Is(err, stop) {
			t.Fatalf("err = %v", err)
		}
	})
}
