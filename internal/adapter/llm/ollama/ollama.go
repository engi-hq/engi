// Package ollama implements port.LLM over the Ollama /api/chat endpoint with
// NDJSON streaming, tool calls and structured output (the format field).
//
// Ollama does not issue tool-call ids, so the client generates unique ones.
// It has no tool_choice: ToolChoiceNone drops the tools, ToolChoiceTool keeps
// only the named tool, ToolChoiceRequired is best effort (tools stay offered).
package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Config configures a Client.
type Config struct {
	BaseURL string
	// APIKey is optional; it authenticates against a proxy in front of Ollama.
	APIKey string
	// HeaderName carries APIKey; empty means Authorization: Bearer.
	HeaderName string
}

// callIDPrefix starts generated tool-call ids.
const callIDPrefix = "call_"

// jsonFormat is Ollama's plain JSON mode value.
const jsonFormat = "json"

// Client is an Ollama port.LLM.
type Client struct {
	cfg    Config
	http   nodesdk.HTTPClient
	nextID atomic.Uint64
}

var _ port.LLM = (*Client)(nil)

// New returns a Client, or an error when the base URL is missing.
func New(cfg Config, httpc nodesdk.HTTPClient) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("ollama: base URL is required")
	}
	return &Client{cfg: cfg, http: httpc}, nil
}

func (c *Client) send(ctx context.Context, body any) (*http.Response, error) {
	req, err := llmkit.JSONRequest(ctx, strings.TrimRight(c.cfg.BaseURL, "/")+"/api/chat", body)
	if err != nil {
		return nil, err
	}
	if c.cfg.APIKey != "" {
		if c.cfg.HeaderName != "" {
			req.Header.Set(c.cfg.HeaderName, c.cfg.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}
	}
	return llmkit.Send(ctx, c.http, port.LLMProviderOllama, req)
}

type wireCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type wireMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []wireCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type requestBody struct {
	Model    string          `json:"model"`
	Messages []wireMessage   `json:"messages"`
	Tools    []wireTool      `json:"tools,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
	Stream   bool            `json:"stream"`
	Options  *wireOptions    `json:"options,omitempty"`
}

var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

func (c *Client) buildBody(req port.LLMRequest, stream bool) (requestBody, error) {
	if req.Model == "" {
		return requestBody{}, errors.New("ollama: model is required")
	}
	body := requestBody{Model: req.Model, Stream: stream}
	if req.Temperature != nil || req.TopP != nil || req.MaxTokens > 0 || len(req.Stop) > 0 {
		body.Options = &wireOptions{Temperature: req.Temperature, TopP: req.TopP, NumPredict: req.MaxTokens, Stop: req.Stop}
	}
	switch req.Format.Kind {
	case port.ResponseJSON:
		body.Format = json.RawMessage(strconv.Quote(jsonFormat))
	case port.ResponseJSONSchema:
		if len(req.Format.Schema) == 0 {
			return requestBody{}, errors.New("ollama: json schema response format needs a schema")
		}
		body.Format = req.Format.Schema
	}
	if req.System != "" {
		body.Messages = append(body.Messages, wireMessage{Role: "system", Content: req.System})
	}
	names := map[string]string{} // tool-call id -> tool name, for tool results
	for _, m := range req.Messages {
		switch m.Role {
		case port.RoleAssistant:
			w := wireMessage{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				names[tc.ID] = tc.Name
				var wc wireCall
				wc.Function.Name, wc.Function.Arguments = tc.Name, llmkit.NormalizeArgs(tc.Arguments)
				w.ToolCalls = append(w.ToolCalls, wc)
			}
			body.Messages = append(body.Messages, w)
		case port.RoleTool:
			content := m.Content
			if m.IsError {
				content = "Error: " + content
			}
			body.Messages = append(body.Messages, wireMessage{Role: "tool", Content: content, ToolName: names[m.ToolCallID]})
		case port.RoleSystem:
			body.Messages = append(body.Messages, wireMessage{Role: "system", Content: m.Content})
		default:
			body.Messages = append(body.Messages, wireMessage{Role: "user", Content: m.Content})
		}
	}
	if req.ToolChoice.Mode != port.ToolChoiceNone {
		for _, t := range req.Tools {
			if req.ToolChoice.Mode == port.ToolChoiceTool && t.Name != req.ToolChoice.Name {
				continue
			}
			wt := wireTool{Type: "function"}
			wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Parameters
			if len(wt.Function.Parameters) == 0 {
				wt.Function.Parameters = emptySchema
			}
			body.Tools = append(body.Tools, wt)
		}
	}
	return body, nil
}

type chunk struct {
	Model   string `json:"model"`
	Message struct {
		Content   string     `json:"content"`
		ToolCalls []wireCall `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

func (c *Client) toCalls(in []wireCall) []port.LLMToolCall {
	var out []port.LLMToolCall
	for _, w := range in {
		out = append(out, port.LLMToolCall{
			ID: callIDPrefix + strconv.FormatUint(c.nextID.Add(1), 10), Name: w.Function.Name,
			Arguments: llmkit.NormalizeArgs(w.Function.Arguments),
		})
	}
	return out
}

func mapDone(reason string, haveCalls bool) port.StopReason {
	switch {
	case haveCalls:
		return port.StopToolUse
	case reason == "length":
		return port.StopMaxTokens
	case reason == "stop" || reason == "":
		return port.StopEnd
	}
	return port.StopOther
}

// Chat implements port.LLM.
func (c *Client) Chat(ctx context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	return c.run(ctx, req, false, nil)
}

// Stream implements port.LLM over Ollama's NDJSON stream.
func (c *Client) Stream(ctx context.Context, req port.LLMRequest, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	return c.run(ctx, req, true, emit)
}

func (c *Client) run(ctx context.Context, req port.LLMRequest, stream bool, emit func(port.StreamEvent) error) (port.LLMResponse, error) {
	body, err := c.buildBody(req, stream)
	if err != nil {
		return port.LLMResponse{}, err
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return port.LLMResponse{}, err
	}
	defer resp.Body.Close()

	var (
		out  port.LLMResponse
		text strings.Builder
		last chunk
		seen bool
	)
	handle := func(ch chunk) error {
		if ch.Error != "" {
			return &port.LLMError{Provider: port.LLMProviderOllama, Message: ch.Error}
		}
		seen = true
		if ch.Model != "" {
			out.Model = ch.Model
		}
		out.ToolCalls = append(out.ToolCalls, c.toCalls(ch.Message.ToolCalls)...)
		if ch.Message.Content != "" {
			text.WriteString(ch.Message.Content)
			if emit != nil {
				if err := emit(port.StreamEvent{TextDelta: ch.Message.Content}); err != nil {
					return err
				}
			}
		}
		if ch.Done {
			last = ch
		}
		return nil
	}
	if stream {
		err = llmkit.ReadNDJSON(resp.Body, func(line []byte) error {
			var ch chunk
			if err := json.Unmarshal(line, &ch); err != nil {
				return &port.LLMError{Provider: port.LLMProviderOllama, Message: "malformed stream line", Err: err}
			}
			return handle(ch)
		})
	} else {
		var ch chunk
		if err = llmkit.DecodeJSON(port.LLMProviderOllama, resp.Body, &ch); err == nil {
			err = handle(ch)
		}
	}
	if err != nil {
		return port.LLMResponse{}, err
	}
	if !seen || !last.Done {
		return port.LLMResponse{}, &port.LLMError{Provider: port.LLMProviderOllama, Message: "response ended before done", Retryable: stream}
	}
	out.Content = text.String()
	out.Usage = port.LLMUsage{InputTokens: last.PromptEvalCount, OutputTokens: last.EvalCount}
	out.StopReason = mapDone(last.DoneReason, len(out.ToolCalls) > 0)
	return out, nil
}
