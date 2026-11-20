// Package openai implements port.LLM over the OpenAI Chat Completions API and
// its Azure OpenAI flavour (deployment URL, api-key header, api-version query).
// Any server that speaks the same API works through a custom base URL.
//
// Response formats map natively: json -> response_format json_object,
// jsonSchema -> response_format json_schema (strict). The token cap is sent as
// max_completion_tokens.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Config configures a Client. Set Azure fields (AzureEndpoint, APIVersion)
// for Azure OpenAI, BaseURL for OpenAI.
type Config struct {
	APIKey string
	// BaseURL is the OpenAI API root including /v1 (OpenAI only).
	BaseURL string
	// Organization is sent as OpenAI-Organization when set (OpenAI only).
	Organization string
	// AzureEndpoint is the Azure resource endpoint; setting it selects Azure.
	AzureEndpoint string
	// APIVersion is the Azure api-version query value.
	APIVersion string
}

// jsonHint is added to the system prompt when JSON mode is requested and no
// message mentions JSON, which the API rejects.
const jsonHint = "Respond with a JSON object."

// schemaNameFallback names an unnamed JSON schema.
const schemaNameFallback = "response"

// Client is an OpenAI or Azure OpenAI port.LLM.
type Client struct {
	cfg      Config
	http     nodesdk.HTTPClient
	provider string
}

var _ port.LLM = (*Client)(nil)

// New returns a Client, or an error when the config is incomplete.
func New(cfg Config, httpc nodesdk.HTTPClient) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("openai: API key is required")
	}
	c := &Client{cfg: cfg, http: httpc, provider: port.LLMProviderOpenAI}
	if cfg.AzureEndpoint != "" {
		if cfg.APIVersion == "" {
			return nil, errors.New("azure openai: API version is required")
		}
		c.provider = port.LLMProviderAzureOpenAI
	} else if cfg.BaseURL == "" {
		return nil, errors.New("openai: base URL is required")
	}
	return c, nil
}

// Provider returns the provider id this client reports in errors and metrics.
func (c *Client) Provider() string { return c.provider }

func (c *Client) endpoint(model string) string {
	if c.provider == port.LLMProviderAzureOpenAI {
		return strings.TrimRight(c.cfg.AzureEndpoint, "/") + "/openai/deployments/" + url.PathEscape(model) +
			"/chat/completions?api-version=" + url.QueryEscape(c.cfg.APIVersion)
	}
	return strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
}

func (c *Client) send(ctx context.Context, model string, body any) (*http.Response, error) {
	req, err := llmkit.JSONRequest(ctx, c.endpoint(model), body)
	if err != nil {
		return nil, err
	}
	if c.provider == port.LLMProviderAzureOpenAI {
		req.Header.Set("api-key", c.cfg.APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		if c.cfg.Organization != "" {
			req.Header.Set("OpenAI-Organization", c.cfg.Organization)
		}
	}
	return llmkit.Send(ctx, c.http, c.provider, req)
}

// Chat implements port.LLM.
func (c *Client) Chat(ctx context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	body, err := c.buildBody(req, false)
	if err != nil {
		return port.LLMResponse{}, err
	}
	resp, err := c.send(ctx, req.Model, body)
	if err != nil {
		return port.LLMResponse{}, err
	}
	defer resp.Body.Close()
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []wireCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage wireUsage `json:"usage"`
	}
	if err := llmkit.DecodeJSON(c.provider, resp.Body, &out); err != nil {
		return port.LLMResponse{}, err
	}
	res := port.LLMResponse{Model: out.Model, Usage: out.Usage.toUsage(), StopReason: port.StopOther}
	if len(out.Choices) == 0 {
		return res, &port.LLMError{Provider: c.provider, Message: "response has no choices"}
	}
	ch := out.Choices[0]
	if ch.Message.Content != nil {
		res.Content = *ch.Message.Content
	}
	for _, tc := range ch.Message.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, tc.toCall())
	}
	res.StopReason = mapFinish(ch.FinishReason, len(res.ToolCalls) > 0)
	return res, nil
}

// wire types

type wireCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (w wireCall) toCall() port.LLMToolCall {
	return port.LLMToolCall{ID: w.ID, Name: w.Function.Name, Arguments: llmkit.NormalizeArgs(json.RawMessage(w.Function.Arguments))}
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (u wireUsage) toUsage() port.LLMUsage {
	return port.LLMUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireResponseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	} `json:"json_schema,omitempty"`
}

type requestBody struct {
	Model          string              `json:"model,omitempty"`
	Messages       []wireMessage       `json:"messages"`
	Tools          []wireTool          `json:"tools,omitempty"`
	ToolChoice     any                 `json:"tool_choice,omitempty"`
	Temperature    *float64            `json:"temperature,omitempty"`
	TopP           *float64            `json:"top_p,omitempty"`
	MaxTokens      int                 `json:"max_completion_tokens,omitempty"`
	Stop           []string            `json:"stop,omitempty"`
	ResponseFormat *wireResponseFormat `json:"response_format,omitempty"`
	Stream         bool                `json:"stream,omitempty"`
	StreamOptions  *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

func strp(s string) *string { return &s }

func (c *Client) buildBody(req port.LLMRequest, stream bool) (requestBody, error) {
	if req.Model == "" {
		return requestBody{}, errors.New(c.provider + ": model is required")
	}
	body := requestBody{
		Temperature: req.Temperature, TopP: req.TopP, MaxTokens: req.MaxTokens, Stop: req.Stop, Stream: stream,
	}
	if c.provider != port.LLMProviderAzureOpenAI {
		body.Model = req.Model
	}
	if stream {
		body.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	system := req.System
	switch req.Format.Kind {
	case port.ResponseJSON:
		body.ResponseFormat = &wireResponseFormat{Type: "json_object"}
		if !mentionsJSON(req) {
			system = strings.TrimSpace(system + "\n\n" + jsonHint)
		}
	case port.ResponseJSONSchema:
		if len(req.Format.Schema) == 0 {
			return requestBody{}, errors.New(c.provider + ": json schema response format needs a schema")
		}
		name := req.Format.Name
		if name == "" {
			name = schemaNameFallback
		}
		body.ResponseFormat = &wireResponseFormat{Type: "json_schema", JSONSchema: &struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict bool            `json:"strict"`
		}{Name: name, Schema: req.Format.Schema, Strict: true}}
	}
	if system != "" {
		body.Messages = append(body.Messages, wireMessage{Role: "system", Content: strp(system)})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, mapMessage(m))
	}
	for _, t := range req.Tools {
		wt := wireTool{Type: "function"}
		wt.Function.Name, wt.Function.Description, wt.Function.Parameters = t.Name, t.Description, t.Parameters
		if len(wt.Function.Parameters) == 0 {
			wt.Function.Parameters = emptySchema
		}
		body.Tools = append(body.Tools, wt)
	}
	if len(body.Tools) > 0 {
		switch req.ToolChoice.Mode {
		case port.ToolChoiceAuto:
			body.ToolChoice = "auto"
		case port.ToolChoiceNone:
			body.ToolChoice = "none"
		case port.ToolChoiceRequired:
			body.ToolChoice = "required"
		case port.ToolChoiceTool:
			body.ToolChoice = map[string]any{"type": "function", "function": map[string]string{"name": req.ToolChoice.Name}}
		}
	}
	return body, nil
}

func mentionsJSON(req port.LLMRequest) bool {
	if strings.Contains(strings.ToLower(req.System), "json") {
		return true
	}
	for _, m := range req.Messages {
		if strings.Contains(strings.ToLower(m.Content), "json") {
			return true
		}
	}
	return false
}

func mapMessage(m port.LLMMessage) wireMessage {
	switch m.Role {
	case port.RoleAssistant:
		w := wireMessage{Role: "assistant"}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			w.Content = strp(m.Content)
		}
		for _, tc := range m.ToolCalls {
			wc := wireCall{ID: tc.ID, Type: "function"}
			wc.Function.Name, wc.Function.Arguments = tc.Name, string(llmkit.NormalizeArgs(tc.Arguments))
			w.ToolCalls = append(w.ToolCalls, wc)
		}
		return w
	case port.RoleTool:
		content := m.Content
		if m.IsError {
			content = "Error: " + content
		}
		return wireMessage{Role: "tool", ToolCallID: m.ToolCallID, Content: strp(content)}
	case port.RoleSystem:
		return wireMessage{Role: "system", Content: strp(m.Content)}
	}
	return wireMessage{Role: "user", Content: strp(m.Content)}
}

func mapFinish(reason string, haveCalls bool) port.StopReason {
	switch reason {
	case "stop":
		if haveCalls {
			return port.StopToolUse
		}
		return port.StopEnd
	case "length":
		return port.StopMaxTokens
	case "tool_calls", "function_call":
		return port.StopToolUse
	}
	return port.StopOther
}
