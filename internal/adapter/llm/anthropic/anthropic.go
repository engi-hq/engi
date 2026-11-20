// Package anthropic implements port.LLM over the Anthropic Messages API
// (https://api.anthropic.com/v1/messages), including SSE streaming and tool use.
//
// Response formats: the API has no plain JSON mode, so ResponseJSON adds a
// system instruction to answer with one JSON object only. ResponseJSONSchema
// is emulated with a forced tool whose input schema is the requested schema;
// the tool's arguments are returned as the reply text. When the request also
// carries tools, forcing is impossible, so the schema is given as a system
// instruction instead (best effort).
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Config configures a Client. All fields are required.
type Config struct {
	APIKey  string
	BaseURL string
	// Version is the anthropic-version header.
	Version string
	// DefaultMaxTokens applies when a request sets no MaxTokens (the API requires one).
	DefaultMaxTokens int
}

// structuredToolName names the forced tool used for ResponseJSONSchema when
// the request gives no schema name.
const structuredToolName = "structured_output"

// jsonInstruction is appended to the system prompt for ResponseJSON.
const jsonInstruction = "Respond with a single valid JSON object only, with no surrounding text or code fences."

// Client is an Anthropic port.LLM.
type Client struct {
	cfg  Config
	http nodesdk.HTTPClient
}

var _ port.LLM = (*Client)(nil)

// New returns a Client, or an error when the config is incomplete.
func New(cfg Config, httpc nodesdk.HTTPClient) (*Client, error) {
	switch {
	case cfg.APIKey == "":
		return nil, errors.New("anthropic: API key is required")
	case cfg.BaseURL == "":
		return nil, errors.New("anthropic: base URL is required")
	case cfg.DefaultMaxTokens <= 0:
		return nil, errors.New("anthropic: default max tokens must be positive")
	}
	return &Client{cfg: cfg, http: httpc}, nil
}

// Chat implements port.LLM.
func (c *Client) Chat(ctx context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	body, forced, err := c.buildBody(req, false)
	if err != nil {
		return port.LLMResponse{}, err
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return port.LLMResponse{}, err
	}
	defer resp.Body.Close()
	var out messageResponse
	if err := llmkit.DecodeJSON(port.LLMProviderAnthropic, resp.Body, &out); err != nil {
		return port.LLMResponse{}, err
	}
	return out.toResponse(forced), nil
}

func (c *Client) send(ctx context.Context, body any) (*http.Response, error) {
	req, err := llmkit.JSONRequest(ctx, strings.TrimRight(c.cfg.BaseURL, "/")+"/v1/messages", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", c.cfg.Version)
	return llmkit.Send(ctx, c.http, port.LLMProviderAnthropic, req)
}

// wire types

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type requestBody struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        string          `json:"system,omitempty"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools,omitempty"`
	ToolChoice    *wireToolChoice `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
}

var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// buildBody maps req to the wire request. forced is the name of the forced
// structured-output tool, "" when none is used.
func (c *Client) buildBody(req port.LLMRequest, stream bool) (body requestBody, forced string, err error) {
	if req.Model == "" {
		return body, "", errors.New("anthropic: model is required")
	}
	body = requestBody{
		Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP,
		StopSequences: req.Stop, Stream: stream,
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = c.cfg.DefaultMaxTokens
	}
	system := []string{}
	if req.System != "" {
		system = append(system, req.System)
	}
	body.Messages, system = mapMessages(req.Messages, system)

	for _, t := range req.Tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = emptySchema
		}
		body.Tools = append(body.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	body.ToolChoice = mapToolChoice(req.ToolChoice, len(body.Tools) > 0)

	switch req.Format.Kind {
	case port.ResponseJSON:
		system = append(system, jsonInstruction)
	case port.ResponseJSONSchema:
		schema := req.Format.Schema
		if len(schema) == 0 {
			return body, "", errors.New("anthropic: json schema response format needs a schema")
		}
		if len(body.Tools) > 0 {
			system = append(system, jsonInstruction+" It must conform to this JSON Schema: "+string(schema))
			break
		}
		forced = req.Format.Name
		if forced == "" {
			forced = structuredToolName
		}
		body.Tools = []wireTool{{Name: forced, Description: "Return the final answer in this structure.", InputSchema: schema}}
		body.ToolChoice = &wireToolChoice{Type: "tool", Name: forced}
	}
	body.System = strings.Join(system, "\n\n")
	return body, forced, nil
}

func mapToolChoice(tc port.ToolChoice, haveTools bool) *wireToolChoice {
	if !haveTools {
		return nil
	}
	switch tc.Mode {
	case port.ToolChoiceNone:
		return &wireToolChoice{Type: "none"}
	case port.ToolChoiceRequired:
		return &wireToolChoice{Type: "any"}
	case port.ToolChoiceTool:
		return &wireToolChoice{Type: "tool", Name: tc.Name}
	case port.ToolChoiceAuto:
		return &wireToolChoice{Type: "auto"}
	}
	return nil
}

// mapMessages converts the conversation. System messages join the system
// prompt; consecutive tool results merge into one user message, as the API
// requires every result of an assistant turn in the next user message.
func mapMessages(in []port.LLMMessage, system []string) (msgs []wireMessage, sys []string) {
	out := msgs
	appendBlocks := func(role string, blocks ...wireBlock) {
		if n := len(out); n > 0 && out[n-1].Role == role && role == "user" {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, wireMessage{Role: role, Content: blocks})
	}
	for _, m := range in {
		switch m.Role {
		case port.RoleSystem:
			system = append(system, m.Content)
		case port.RoleUser:
			appendBlocks("user", wireBlock{Type: "text", Text: m.Content})
		case port.RoleAssistant:
			var blocks []wireBlock
			if m.Content != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, wireBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: llmkit.NormalizeArgs(tc.Arguments)})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, wireBlock{Type: "text", Text: " "})
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})
		case port.RoleTool:
			appendBlocks("user", wireBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content, IsError: m.IsError})
		}
	}
	return out, system
}

// response types

type usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u usage) input() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

type messageResponse struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage usage `json:"usage"`
}

func (m messageResponse) toResponse(forced string) port.LLMResponse {
	out := port.LLMResponse{Model: m.Model, StopReason: mapStop(m.StopReason),
		Usage: port.LLMUsage{InputTokens: m.Usage.input(), OutputTokens: m.Usage.OutputTokens}}
	var text strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			if forced != "" && b.Name == forced {
				text.WriteString(string(llmkit.NormalizeArgs(b.Input)))
				continue
			}
			out.ToolCalls = append(out.ToolCalls, port.LLMToolCall{ID: b.ID, Name: b.Name, Arguments: llmkit.NormalizeArgs(b.Input)})
		}
	}
	out.Content = text.String()
	if forced != "" && len(out.ToolCalls) == 0 && out.StopReason == port.StopToolUse {
		out.StopReason = port.StopEnd
	}
	return out
}

func mapStop(s string) port.StopReason {
	switch s {
	case "end_turn":
		return port.StopEnd
	case "max_tokens":
		return port.StopMaxTokens
	case "tool_use":
		return port.StopToolUse
	case "stop_sequence":
		return port.StopStopSequence
	}
	return port.StopOther
}

// errorf builds a non-retryable stream protocol error.
func errorf(format string, args ...any) error {
	return &port.LLMError{Provider: port.LLMProviderAnthropic, Message: fmt.Sprintf(format, args...)}
}
