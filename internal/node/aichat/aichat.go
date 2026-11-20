// Package aichat implements the AI Chat node (engi.ai.chat): one model call
// per input item with an optional system prompt and a text, JSON or
// JSON-schema reply. The model client comes from the injected port.LLMFactory.
package aichat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/node/aikit"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.chat"

// Config is what the node needs from the host.
type Config struct {
	// Factory builds the model client. Nil makes the node fail with
	// nodesdk.ErrUnavailable.
	Factory port.LLMFactory
	// DefaultModels maps a provider id to the model used when the node sets none.
	DefaultModels map[string]string
}

// Node is the AI Chat node.
type Node struct{ cfg Config }

// New returns an AI Chat node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	schemaOnly := nodekit.Show("responseFormat", aikit.FormatJSONSchema)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "AI Chat", Group: nodesdk.GroupAI, Icon: "sparkles",
		Description: "Send a prompt to a chat model (Anthropic, OpenAI, Azure OpenAI or Ollama) for each item and get text or JSON back.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: aikit.ProviderCredentials(),
		Properties: []nodesdk.Property{
			aikit.ProviderProperty(),
			{Name: "model", DisplayName: "Model", Type: nodesdk.PropString,
				Description: "Model name, for example claude-sonnet-5-5, claude-opus-5-5, claude-haiku-5-5, gpt-4o-mini or llama3.2:3b. Empty uses the provider default (nodes.ai.*DefaultModel). For Azure OpenAI this is the deployment name and is required."},
			{Name: "systemPrompt", DisplayName: "System prompt", Type: nodesdk.PropString},
			{Name: "message", DisplayName: "Message", Type: nodesdk.PropString, Required: true,
				Description: "The user message. Use an expression to build it from the item."},
			{Name: "temperature", DisplayName: "Temperature", Type: nodesdk.PropNumber, Description: "Empty uses the provider default."},
			{Name: "maxTokens", DisplayName: "Max tokens", Type: nodesdk.PropNumber,
				Description: "Reply token cap. Empty uses the provider default (nodes.ai.defaultMaxTokens for Anthropic)."},
			{Name: "responseFormat", DisplayName: "Response format", Type: nodesdk.PropOptions, Default: aikit.FormatText,
				Options: []nodesdk.Option{
					nodekit.Opt(aikit.FormatText, "Text"), nodekit.Opt(aikit.FormatJSON, "JSON"), nodekit.Opt(aikit.FormatJSONSchema, "JSON schema"),
				}},
			{Name: "schemaName", DisplayName: "Schema name", Type: nodesdk.PropString, Default: aikit.DefaultSchemaName, DisplayOptions: schemaOnly},
			{Name: "schema", DisplayName: "JSON schema", Type: nodesdk.PropJSON, Required: true, DisplayOptions: schemaOnly,
				Description: "A JSON Schema object the reply must conform to."},
			{Name: "options", DisplayName: "Options", Type: nodesdk.PropCollection, Children: []nodesdk.Property{
				{Name: "topP", DisplayName: "Top P", Type: nodesdk.PropNumber},
				{Name: "stopSequences", DisplayName: "Stop sequences", Type: nodesdk.PropString, Description: "Comma separated."},
			}},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	if n.cfg.Factory == nil {
		return nodesdk.Output{}, fmt.Errorf("ai chat: %w", nodesdk.ErrUnavailable)
	}
	provider, err := nodesdk.ParamString(ctx, in, "provider", 0)
	if err != nil {
		return nodesdk.Output{}, err
	}
	credType, ok := aikit.CredentialTypes[provider]
	if !ok {
		return nodesdk.Output{}, fmt.Errorf("unknown provider %q", provider)
	}
	cred, err := in.Credential(ctx, credType)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", credType, err)
	}
	llm, err := n.cfg.Factory.New(provider, cred, in.HTTP())
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("creating %s client: %w", provider, err)
	}
	out := make([]nodesdk.Item, 0, len(items))
	for i := range items {
		req, format, err := n.request(ctx, in, provider, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		resp, err := llm.Chat(ctx, req)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("calling %s: %w", provider, err))
		}
		res, err := result(resp, format)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, nodekit.Paired(nodesdk.Item{JSON: res}, i))
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// request reads the per-item parameters into a chat request.
func (n *Node) request(ctx context.Context, in nodesdk.Input, provider string, i int) (port.LLMRequest, string, error) {
	var req port.LLMRequest
	var err error
	if req.Model, err = nodesdk.ParamString(ctx, in, "model", i); err != nil {
		return req, "", err
	}
	if req.Model = strings.TrimSpace(req.Model); req.Model == "" {
		req.Model = n.cfg.DefaultModels[provider]
	}
	if req.Model == "" {
		return req, "", errors.New("model is required")
	}
	if req.System, err = nodesdk.ParamString(ctx, in, "systemPrompt", i); err != nil {
		return req, "", err
	}
	msg, err := nodesdk.ParamString(ctx, in, "message", i)
	if err != nil {
		return req, "", err
	}
	if strings.TrimSpace(msg) == "" {
		return req, "", errors.New("message is required")
	}
	req.Messages = []port.LLMMessage{{Role: port.RoleUser, Content: msg}}
	if req.Temperature, err = optFloat(ctx, in, "temperature", i); err != nil {
		return req, "", err
	}
	if req.MaxTokens, err = nodesdk.ParamInt(ctx, in, "maxTokens", i); err != nil {
		return req, "", err
	}
	if req.MaxTokens < 0 {
		return req, "", errors.New("maxTokens must not be negative")
	}
	opts, err := nodekit.ParamMap(ctx, in, "options", i)
	if err != nil {
		return req, "", err
	}
	if v, ok := opts["topP"]; ok && v != nil && v != "" {
		f, err := nodekit.ToFloat(v, true)
		if err != nil {
			return req, "", fmt.Errorf("option topP: %w", err)
		}
		req.TopP = &f
	}
	if req.Stop, err = nodekit.StringList(opts["stopSequences"]); err != nil {
		return req, "", fmt.Errorf("option stopSequences: %w", err)
	}
	var format string
	if req.Format, format, err = aikit.ReadFormat(ctx, in, i); err != nil {
		return req, "", err
	}
	return req, format, nil
}

// optFloat reads an optional number; empty or missing is nil.
func optFloat(ctx context.Context, in nodesdk.Input, name string, i int) (*float64, error) {
	v, err := in.Param(ctx, name, i)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	if v == nil || v == "" {
		return nil, nil
	}
	f, err := nodekit.ToFloat(v, true)
	if err != nil {
		return nil, fmt.Errorf("parameter %q: %w", name, err)
	}
	return &f, nil
}

// result builds the output item JSON from the model reply.
func result(resp port.LLMResponse, format string) (map[string]any, error) {
	res := map[string]any{
		"text": resp.Content, "model": resp.Model, "stopReason": string(resp.StopReason),
		"usage": map[string]any{
			"inputTokens": float64(resp.Usage.InputTokens), "outputTokens": float64(resp.Usage.OutputTokens),
			"totalTokens": float64(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}
	if format == aikit.FormatText {
		return res, nil
	}
	parsed, err := aikit.ParseJSONReply(resp.Content)
	if err != nil {
		if resp.StopReason == port.StopMaxTokens {
			return nil, errors.New("reply was cut off by the token limit before the JSON was complete; raise maxTokens")
		}
		return nil, fmt.Errorf("reply is not valid JSON: %w", err)
	}
	res["json"] = parsed
	return res, nil
}
