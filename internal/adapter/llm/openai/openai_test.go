package openai_test

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

	"github.com/engi-hq/engi/internal/adapter/llm/openai"
	"github.com/engi-hq/engi/internal/port"
)

type captured struct {
	header http.Header
	path   string
	query  string
	body   map[string]any
}

func server(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *captured) {
	t.Helper()
	rec := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.header, rec.path, rec.query = r.Header.Clone(), r.URL.Path, r.URL.RawQuery
		rec.body = nil
		_ = json.Unmarshal(b, &rec.body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func newOpenAI(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*openai.Client, *captured) {
	t.Helper()
	srv, rec := server(t, handler)
	c, err := openai.New(openai.Config{APIKey: "sk-test", BaseURL: srv.URL + "/v1", Organization: "org-1"}, srv.Client())
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

const textReply = `{"model":"gpt-4o-mini-2024","choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":2}}`

func TestChatText(t *testing.T) {
	c, rec := newOpenAI(t, reply(textReply))
	temp, top := 0.5, 0.9
	resp, err := c.Chat(context.Background(), port.LLMRequest{
		Model: "gpt-4o-mini", System: "sys", Temperature: &temp, TopP: &top, MaxTokens: 50, Stop: []string{"X"},
		Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi there" || resp.StopReason != port.StopEnd || resp.Model != "gpt-4o-mini-2024" || resp.Usage != (port.LLMUsage{InputTokens: 9, OutputTokens: 2}) {
		t.Fatalf("resp = %+v", resp)
	}
	if rec.path != "/v1/chat/completions" || rec.header.Get("Authorization") != "Bearer sk-test" || rec.header.Get("OpenAI-Organization") != "org-1" {
		t.Fatalf("path %q headers %v", rec.path, rec.header)
	}
	msgs := rec.body["messages"].([]any)
	if rec.body["model"] != "gpt-4o-mini" || rec.body["max_completion_tokens"] != float64(50) || msgs[0].(map[string]any)["role"] != "system" || len(msgs) != 2 {
		t.Fatalf("body = %v", rec.body)
	}
}

func TestFinishReasons(t *testing.T) {
	for reason, want := range map[string]port.StopReason{"stop": port.StopEnd, "length": port.StopMaxTokens, "tool_calls": port.StopToolUse, "content_filter": port.StopOther} {
		c, _ := newOpenAI(t, reply(`{"choices":[{"message":{"content":""},"finish_reason":"`+reason+`"}]}`))
		resp, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
		if err != nil || resp.StopReason != want {
			t.Errorf("%s: %q %v, want %q", reason, resp.StopReason, err, want)
		}
	}
}

func TestToolCalls(t *testing.T) {
	c, rec := newOpenAI(t, reply(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Oslo\"}"}},{"id":"call_2","type":"function","function":{"name":"noargs","arguments":""}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`))
	resp, err := c.Chat(context.Background(), port.LLMRequest{
		Model: "m", Tools: []port.ToolDefinition{{Name: "weather", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "noargs"}},
		ToolChoice: port.ToolChoice{Mode: port.ToolChoiceTool, Name: "weather"},
		Messages: []port.LLMMessage{
			{Role: port.RoleUser, Content: "q"},
			{Role: port.RoleAssistant, ToolCalls: []port.LLMToolCall{{ID: "x", Name: "weather", Arguments: json.RawMessage(`{"city":"Rome"}`)}}},
			{Role: port.RoleTool, ToolCallID: "x", Content: "bad input", IsError: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != port.StopToolUse || len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "call_1" ||
		string(resp.ToolCalls[0].Arguments) != `{"city":"Oslo"}` || string(resp.ToolCalls[1].Arguments) != `{}` {
		t.Fatalf("resp = %+v", resp)
	}
	msgs := rec.body["messages"].([]any)
	asst, tool := msgs[1].(map[string]any), msgs[2].(map[string]any)
	if asst["content"] != nil || asst["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"city":"Rome"}` {
		t.Fatalf("assistant = %v", asst)
	}
	if tool["tool_call_id"] != "x" || tool["content"] != "Error: bad input" {
		t.Fatalf("tool = %v", tool)
	}
	tc := rec.body["tool_choice"].(map[string]any)
	if tc["type"] != "function" || tc["function"].(map[string]any)["name"] != "weather" {
		t.Fatalf("tool_choice = %v", tc)
	}
}

func TestToolChoiceModes(t *testing.T) {
	for mode, want := range map[port.ToolChoiceMode]string{port.ToolChoiceAuto: "auto", port.ToolChoiceNone: "none", port.ToolChoiceRequired: "required"} {
		c, rec := newOpenAI(t, reply(textReply))
		if _, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Tools: []port.ToolDefinition{{Name: "t"}}, ToolChoice: port.ToolChoice{Mode: mode}}); err != nil {
			t.Fatal(err)
		}
		if rec.body["tool_choice"] != want {
			t.Errorf("%s: tool_choice %v", mode, rec.body["tool_choice"])
		}
	}
}

func TestResponseFormats(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`)
	tests := []struct {
		name   string
		req    port.LLMRequest
		check  func(t *testing.T, body map[string]any)
		hasErr bool
	}{
		{"text sends nothing", port.LLMRequest{Model: "m"}, func(t *testing.T, b map[string]any) {
			if _, ok := b["response_format"]; ok {
				t.Fatal("response_format sent")
			}
		}, false},
		{"json adds json_object and a hint when JSON is not mentioned", port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSON}, Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "list"}}}, func(t *testing.T, b map[string]any) {
			if b["response_format"].(map[string]any)["type"] != "json_object" {
				t.Fatalf("body = %v", b)
			}
			if !strings.Contains(b["messages"].([]any)[0].(map[string]any)["content"].(string), "JSON") {
				t.Fatal("no JSON hint in system message")
			}
		}, false},
		{"json leaves the prompt alone when it mentions JSON", port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSON}, Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "give json"}}}, func(t *testing.T, b map[string]any) {
			if len(b["messages"].([]any)) != 1 {
				t.Fatalf("messages = %v", b["messages"])
			}
		}, false},
		{"jsonSchema is strict and named", port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema, Name: "num", Schema: schema}}, func(t *testing.T, b map[string]any) {
			rf := b["response_format"].(map[string]any)
			js := rf["json_schema"].(map[string]any)
			if rf["type"] != "json_schema" || js["name"] != "num" || js["strict"] != true || js["schema"] == nil {
				t.Fatalf("response_format = %v", rf)
			}
		}, false},
		{"jsonSchema defaults the name", port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema, Schema: schema}}, func(t *testing.T, b map[string]any) {
			if b["response_format"].(map[string]any)["json_schema"].(map[string]any)["name"] == "" {
				t.Fatal("empty schema name")
			}
		}, false},
		{"jsonSchema without schema fails", port.LLMRequest{Model: "m", Format: port.ResponseFormat{Kind: port.ResponseJSONSchema}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newOpenAI(t, reply(textReply))
			_, err := c.Chat(context.Background(), tt.req)
			if (err != nil) != tt.hasErr {
				t.Fatalf("err = %v", err)
			}
			if tt.check != nil {
				tt.check(t, rec.body)
			}
		})
	}
}

func TestAzureRequestShape(t *testing.T) {
	srv, rec := server(t, reply(textReply))
	c, err := openai.New(openai.Config{APIKey: "az-key", AzureEndpoint: srv.URL + "/", APIVersion: "2024-10-21"}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider() != port.LLMProviderAzureOpenAI {
		t.Fatalf("provider = %q", c.Provider())
	}
	if _, err := c.Chat(context.Background(), port.LLMRequest{Model: "my deploy", Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/openai/deployments/my deploy/chat/completions" || rec.query != "api-version=2024-10-21" {
		t.Fatalf("path %q query %q", rec.path, rec.query)
	}
	if rec.header.Get("api-key") != "az-key" || rec.header.Get("Authorization") != "" {
		t.Fatalf("headers = %v", rec.header)
	}
	if _, ok := rec.body["model"]; ok {
		t.Fatal("Azure body must not carry model")
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryHdr  string
		body      string
		retryable bool
		after     time.Duration
		message   string
	}{
		{"rate limit", 429, "2", `{"error":{"message":"Rate limit reached","type":"requests"}}`, true, 2 * time.Second, "Rate limit reached"},
		{"unavailable", 503, "", `{"error":{"message":"down"}}`, true, 0, "down"},
		{"invalid", 400, "", `{"error":{"message":"bad model"}}`, false, 0, "bad model"},
		{"auth", 401, "", `{"error":{"message":"Incorrect API key"}}`, false, 0, "Incorrect API key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newOpenAI(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.retryHdr != "" {
					w.Header().Set("Retry-After", tt.retryHdr)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
			var le *port.LLMError
			if !errors.As(err, &le) || le.Status != tt.status || le.Retryable != tt.retryable || le.RetryAfter != tt.after || le.Message != tt.message || le.Provider != port.LLMProviderOpenAI {
				t.Fatalf("err = %v (%+v)", err, le)
			}
		})
	}
}

func TestNewValidates(t *testing.T) {
	for name, cfg := range map[string]openai.Config{
		"no key": {BaseURL: "u"}, "no base": {APIKey: "k"}, "azure without version": {APIKey: "k", AzureEndpoint: "e"},
	} {
		if _, err := openai.New(cfg, http.DefaultClient); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	c, _ := newOpenAI(t, reply(textReply))
	if _, err := c.Chat(context.Background(), port.LLMRequest{}); err == nil {
		t.Error("empty model must fail")
	}
	empty, _ := newOpenAI(t, reply(`{"choices":[]}`))
	if _, err := empty.Chat(context.Background(), port.LLMRequest{Model: "m"}); err == nil {
		t.Error("no choices must fail")
	}
}

const sseBody = `data: {"model":"gpt-4o-mini","choices":[{"delta":{"role":"assistant","content":""}}]}

data: {"choices":[{"delta":{"content":"Hel"}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"calc","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"other","arguments":"{}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":6}}

data: [DONE]

`

func TestStream(t *testing.T) {
	c, rec := newOpenAI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseBody)
	})
	var deltas []string
	resp, err := c.Stream(context.Background(), port.LLMRequest{Model: "gpt-4o-mini"}, func(e port.StreamEvent) error {
		deltas = append(deltas, e.TextDelta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["stream"] != true || rec.body["stream_options"].(map[string]any)["include_usage"] != true {
		t.Fatalf("body = %v", rec.body)
	}
	if strings.Join(deltas, "|") != "Hel|lo" || resp.Content != "Hello" || resp.Model != "gpt-4o-mini" {
		t.Fatalf("deltas %v resp %+v", deltas, resp)
	}
	if resp.StopReason != port.StopToolUse || resp.Usage != (port.LLMUsage{InputTokens: 11, OutputTokens: 6}) {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID != "call_a" || string(resp.ToolCalls[0].Arguments) != `{"a":1}` || resp.ToolCalls[1].Name != "other" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
}

func TestStreamFailures(t *testing.T) {
	run := func(t *testing.T, h func(http.ResponseWriter, *http.Request), emit func(port.StreamEvent) error) error {
		c, _ := newOpenAI(t, h)
		_, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, emit)
		return err
	}
	nop := func(port.StreamEvent) error { return nil }
	t.Run("http 429", func(t *testing.T) {
		if err := run(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }, nop); !port.IsRetryableLLMError(err) {
			t.Fatal(err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		err := run(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		}, nop)
		if !port.IsRetryableLLMError(err) {
			t.Fatal(err)
		}
	})
	t.Run("error event", func(t *testing.T) {
		err := run(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"kaput\"}}\n\n")
		}, nop)
		if err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatal(err)
		}
	})
	t.Run("emit error", func(t *testing.T) {
		stop := errors.New("stop")
		err := run(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, sseBody) }, func(port.StreamEvent) error { return stop })
		if !errors.Is(err, stop) {
			t.Fatal(err)
		}
	})
}
