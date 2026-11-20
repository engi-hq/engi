// Package aimodel implements the AI Model sub-node (engi.ai.model): it picks a
// provider, credential and model and supplies them to the node it is connected
// to (the AI Agent) through the ai.model sub-port. It never runs as a step.
package aimodel

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
const Type = "engi.ai.model"

// Config is what the node needs from the host.
type Config struct {
	// Factory builds the model client. Nil makes Supply fail with
	// nodesdk.ErrUnavailable.
	Factory port.LLMFactory
	// DefaultModels maps a provider id to the model used when the node sets none.
	DefaultModels map[string]string
}

// Node is the AI Model sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns an AI Model node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "AI Model", Group: nodesdk.GroupAI, Icon: "sparkles",
		Description: "The chat model an AI Agent uses: provider, credential, model and sampling options.",
		Outputs:     []nodesdk.PortSpec{{Name: "model", Kind: nodesdk.PortAIModel, DisplayName: "Model"}},
		Credentials: aikit.ProviderCredentials(),
		Properties: []nodesdk.Property{
			aikit.ProviderProperty(),
			{Name: "model", DisplayName: "Model", Type: nodesdk.PropString,
				Description: "Model name, for example claude-sonnet-5-5, claude-opus-5-5, claude-haiku-5-5, gpt-4o-mini or llama3.2:3b. Empty uses the provider default (nodes.ai.*DefaultModel). For Azure OpenAI this is the deployment name and is required."},
			{Name: "temperature", DisplayName: "Temperature", Type: nodesdk.PropNumber, Description: "Empty uses the provider default."},
			{Name: "maxTokens", DisplayName: "Max tokens", Type: nodesdk.PropNumber,
				Description: "Reply token cap per model call. Empty uses the provider default (nodes.ai.defaultMaxTokens for Anthropic)."},
			{Name: "options", DisplayName: "Options", Type: nodesdk.PropCollection, Children: []nodesdk.Property{
				{Name: "topP", DisplayName: "Top P", Type: nodesdk.PropNumber},
				{Name: "stopSequences", DisplayName: "Stop sequences", Type: nodesdk.PropString, Description: "Comma separated."},
			}},
		},
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("ai model: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.ModelProvider.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	if n.cfg.Factory == nil {
		return nil, fmt.Errorf("ai model: %w", nodesdk.ErrUnavailable)
	}
	provider, err := nodesdk.ParamString(ctx, in, "provider", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // SDK errors are descriptive
	}
	credType, ok := aikit.CredentialTypes[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	req, err := n.template(ctx, in, provider)
	if err != nil {
		return nil, err
	}
	cred, err := in.Credential(ctx, credType)
	if err != nil {
		return nil, fmt.Errorf("loading credential %q: %w", credType, err)
	}
	llm, err := n.cfg.Factory.New(provider, cred, in.HTTP())
	if err != nil {
		return nil, fmt.Errorf("creating %s client: %w", provider, err)
	}
	return modelProvider{llm: llm, req: req}, nil
}

// template reads the model settings into a request the agent completes.
func (n *Node) template(ctx context.Context, in nodesdk.Input, provider string) (port.LLMRequest, error) {
	var req port.LLMRequest
	var err error
	if req.Model, err = nodesdk.ParamString(ctx, in, "model", 0); err != nil {
		return req, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if req.Model = strings.TrimSpace(req.Model); req.Model == "" {
		req.Model = n.cfg.DefaultModels[provider]
	}
	if req.Model == "" {
		return req, errors.New("model is required")
	}
	if req.Temperature, err = optFloat(ctx, in, "temperature"); err != nil {
		return req, err
	}
	if req.MaxTokens, err = nodesdk.ParamInt(ctx, in, "maxTokens", 0); err != nil {
		return req, err //nolint:wrapcheck // SDK errors are descriptive
	}
	if req.MaxTokens < 0 {
		return req, errors.New("maxTokens must not be negative")
	}
	opts, err := nodekit.ParamMap(ctx, in, "options", 0)
	if err != nil {
		return req, err //nolint:wrapcheck // descriptive
	}
	if v, ok := opts["topP"]; ok && v != nil && v != "" {
		f, err := nodekit.ToFloat(v, true)
		if err != nil {
			return req, fmt.Errorf("option topP: %w", err)
		}
		req.TopP = &f
	}
	if req.Stop, err = nodekit.StringList(opts["stopSequences"]); err != nil {
		return req, fmt.Errorf("option stopSequences: %w", err)
	}
	return req, nil
}

func optFloat(ctx context.Context, in nodesdk.Input, name string) (*float64, error) {
	v, err := in.Param(ctx, name, 0)
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

type modelProvider struct {
	llm port.LLM
	req port.LLMRequest
}

// Model implements port.ModelProvider.
func (m modelProvider) Model(context.Context) (port.LLM, port.LLMRequest, error) {
	return m.llm, m.req, nil
}
