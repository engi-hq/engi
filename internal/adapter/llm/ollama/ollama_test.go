package ollama_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/llm/ollama"
	"github.com/engi-hq/engi/internal/port"
)

type captured struct {
	header http.Header
	path   string
	body   map[string]any
}

func newClient(t *testing.T, cfg ollama.Config, handler func(http.ResponseWriter, *http.Request)) (*ollama.Client, *captured) {
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
	cfg.BaseURL = srv.URL
	c, err := ollama.New(cfg, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

const textReply = `{"model":"llama3.2:3b","message":{"role":"assistant","content":"pong"},"done":true,"done_reason":"stop","prompt_eval_count":8,"eval_count":2}`

func TestChatText(t *testing.T) {
	c, rec := newClient(t, ollama.Config{}, reply(textReply))
	temp := 0.1
	resp, err := c.Chat(context.Background(), port.LLMRequest{
		Model: "llama3.2:3b", System: "sys", Temperature: &temp, MaxTokens: 20, Stop: []string{"x"},
		Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "pong" || resp.StopReason != port.StopEnd || resp.Usage != (port.LLMUsage{InputTokens: 8, OutputTokens: 2}) || resp.Model != "llama3.2:3b" {
		t.Fatalf("resp = %+v", resp)
	}
	opts := rec.body["options"].(map[string]any)
	if rec.path != "/api/chat" || rec.body["stream"] != false || opts["num_predict"] != float64(20) || opts["temperature"] != 0.1 {
		t.Fatalf("path %q body %v", rec.path, rec.body)
	}
	if rec.header.Get("Authorization") != "" {
		t.Fatal("auth header sent without a key")
	}
	if resp, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"}); err != nil || resp.Content == "" {
		t.Fatal(err)
	}
	if _, ok := rec.body["options"]; ok {
		t.Fatal("empty options must be omitted")
	}
}

func TestDoneReasons(t *testing.T) {
	for reason, want := range map[string]port.StopReason{"stop": port.StopEnd, "length": port.StopMaxTokens, "load": port.StopOther} {
		c, _ := newClient(t, ollama.Config{}, reply(`{"message":{"content":"a"},"done":true,"done_reason":"`+reason+`"}`))
		resp, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
		if err != nil || resp.StopReason != want {
			t.Errorf("%s: %q %v", reason, resp.StopReason, err)
		}
	}
}

func TestAuthHeaders(t *testing.T) {
	c, rec := newClient(t, ollama.Config{APIKey: "tok"}, reply(textReply))
	_, _ = c.Chat(context.Background(), port.LLMRequest{Model: "m"})
	if rec.header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("headers = %v", rec.header)
	}
	c, rec = newClient(t, ollama.Config{APIKey: "tok", HeaderName: "X-Api-Key"}, reply(textReply))
	_, _ = c.Chat(context.Background(), port.LLMRequest{Model: "m"})
	if rec.header.Get("X-Api-Key") != "tok" || rec.header.Get("Authorization") != "" {
		t.Fatalf("headers = %v", rec.header)
	}
}

func TestToolCalls(t *testing.T) {
	c, rec := newClient(t, ollama.Config{}, reply(`{"model":"m","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"weather","arguments":{"city":"Oslo"}}},{"function":{"name":"noargs"}}]},"done":true,"done_reason":"stop"}`))
	req := port.LLMRequest{
		Model: "m", Tools: []port.ToolDefinition{{Name: "weather", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "other"}},
		Messages: []port.LLMMessage{
			{Role: port.RoleUser, Content: "q"},
			{Role: port.RoleAssistant, ToolCalls: []port.LLMToolCall{{ID: "id1", Name: "weather", Arguments: json.RawMessage(`{"city":"Rome"}`)}}},
			{Role: port.RoleTool, ToolCallID: "id1", Content: "oops", IsError: true},
		},
	}
	resp, err := c.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != port.StopToolUse || len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "weather" ||
		string(resp.ToolCalls[0].Arguments) != `{"city":"Oslo"}` || string(resp.ToolCalls[1].Arguments) != `{}` {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.ToolCalls[0].ID == "" || resp.ToolCalls[0].ID == resp.ToolCalls[1].ID {
		t.Fatalf("ids must be unique and set: %+v", resp.ToolCalls)
	}
	msgs := rec.body["messages"].([]any)
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_name"] != "weather" || tool["content"] != "Error: oops" {
		t.Fatalf("tool message = %v", tool)
	}
	if len(rec.body["tools"].([]any)) != 2 {
		t.Fatalf("tools = %v", rec.body["tools"])
	}
	// Tool choice emulation.
	req.ToolChoice = port.ToolChoice{Mode: port.ToolChoiceNone}
	_, _ = c.Chat(context.Background(), req)
	if _, ok := rec.body["tools"]; ok {
		t.Fatal("ToolChoiceNone must drop tools")
	}
	req.ToolChoice = port.ToolChoice{Mode: port.ToolChoiceTool, Name: "other"}
	_, _ = c.Chat(context.Background(), req)
	if tools := rec.body["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "other" {
		t.Fatalf("tools = %v", tools)
	}
}

func TestFormat(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	tests := []struct {
		name string
		f    port.ResponseFormat
		want string
		fail bool
	}{
		{"text", port.ResponseFormat{}, "", false},
		{"json", port.ResponseFormat{Kind: port.ResponseJSON}, `"json"`, false},
		{"schema", port.ResponseFormat{Kind: port.ResponseJSONSchema, Schema: schema}, string(schema), false},
		{"schema missing", port.ResponseFormat{Kind: port.ResponseJSONSchema}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newClient(t, ollama.Config{}, reply(textReply))
			_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m", Format: tt.f})
			if (err != nil) != tt.fail {
				t.Fatalf("err = %v", err)
			}
			if tt.fail {
				return
			}
			got, _ := json.Marshal(rec.body["format"])
			if tt.want == "" {
				if rec.body["format"] != nil {
					t.Fatalf("format = %s", got)
				}
				return
			}
			var a, b any
			_ = json.Unmarshal(got, &a)
			_ = json.Unmarshal([]byte(tt.want), &b)
			ga, _ := json.Marshal(a)
			gb, _ := json.Marshal(b)
			if !bytes.Equal(ga, gb) {
				t.Fatalf("format = %s, want %s", got, tt.want)
			}
		})
	}
}

const ndjson = `{"model":"llama3.2:3b","message":{"role":"assistant","content":"Hel"},"done":false}
{"model":"llama3.2:3b","message":{"role":"assistant","content":"lo"},"done":false}
{"model":"llama3.2:3b","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"calc","arguments":{"a":1}}}]},"done":false}
{"model":"llama3.2:3b","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":9}
`

func TestStream(t *testing.T) {
	c, rec := newClient(t, ollama.Config{}, reply(ndjson))
	var deltas []string
	resp, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(e port.StreamEvent) error {
		deltas = append(deltas, e.TextDelta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["stream"] != true || strings.Join(deltas, "|") != "Hel|lo" || resp.Content != "Hello" {
		t.Fatalf("deltas %v resp %+v", deltas, resp)
	}
	if resp.StopReason != port.StopToolUse || resp.Usage != (port.LLMUsage{InputTokens: 5, OutputTokens: 9}) ||
		len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
		message   string
	}{
		{"model missing", 404, `{"error":"model 'x' not found"}`, false, "model 'x' not found"},
		{"overloaded", 503, `{"error":"busy"}`, true, "busy"},
		{"rate limited", 429, `{"error":"slow"}`, true, "slow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(t, ollama.Config{}, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Chat(context.Background(), port.LLMRequest{Model: "m"})
			var le *port.LLMError
			if !errors.As(err, &le) || le.Status != tt.status || le.Retryable != tt.retryable || le.Message != tt.message || le.Provider != port.LLMProviderOllama {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("error line in stream", func(t *testing.T) {
		c, _ := newClient(t, ollama.Config{}, reply(`{"error":"model runner crashed"}`+"\n"))
		_, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "crashed") {
			t.Fatal(err)
		}
	})
	t.Run("truncated stream", func(t *testing.T) {
		c, _ := newClient(t, ollama.Config{}, reply(`{"message":{"content":"a"},"done":false}`+"\n"))
		if _, err := c.Stream(context.Background(), port.LLMRequest{Model: "m"}, func(port.StreamEvent) error { return nil }); !port.IsRetryableLLMError(err) {
			t.Fatal(err)
		}
	})
	t.Run("validation", func(t *testing.T) {
		if _, err := ollama.New(ollama.Config{}, http.DefaultClient); err == nil {
			t.Error("missing base URL must fail")
		}
		c, _ := newClient(t, ollama.Config{}, reply(textReply))
		if _, err := c.Chat(context.Background(), port.LLMRequest{}); err == nil {
			t.Error("missing model must fail")
		}
	})
}

// TestLive talks to a real Ollama server. It runs only when both
// ENGI_TEST_OLLAMA_URL and ENGI_TEST_OLLAMA_MODEL are set (for example
// http://localhost:11434 and llama3.2:3b).
func TestLive(t *testing.T) {
	url, model := os.Getenv("ENGI_TEST_OLLAMA_URL"), os.Getenv("ENGI_TEST_OLLAMA_MODEL")
	if url == "" || model == "" {
		t.Skip("set ENGI_TEST_OLLAMA_URL and ENGI_TEST_OLLAMA_MODEL to run")
	}
	c, err := ollama.New(ollama.Config{BaseURL: url}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := port.LLMRequest{Model: model, MaxTokens: 64, Messages: []port.LLMMessage{{Role: port.RoleUser, Content: "Say hello in one word."}}}
	resp, err := c.Chat(ctx, req)
	if err != nil || strings.TrimSpace(resp.Content) == "" || resp.Usage.OutputTokens == 0 {
		t.Fatalf("chat: %+v %v", resp, err)
	}
	var got strings.Builder
	sresp, err := c.Stream(ctx, req, func(e port.StreamEvent) error { got.WriteString(e.TextDelta); return nil })
	if err != nil || got.String() != sresp.Content || sresp.Content == "" {
		t.Fatalf("stream: %+v %v", sresp, err)
	}
	req.Format = port.ResponseFormat{Kind: port.ResponseJSON}
	req.Messages = []port.LLMMessage{{Role: port.RoleUser, Content: `Reply with the JSON object {"ok": true}.`}}
	jresp, err := c.Chat(ctx, req)
	if err != nil || !json.Valid([]byte(jresp.Content)) {
		t.Fatalf("json: %+v %v", jresp, err)
	}
}
