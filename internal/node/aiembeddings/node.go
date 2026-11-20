// Package aiembeddings implements the Embeddings sub-node (engi.ai.embeddings).
// It supplies a port.Embedder for an OpenAI, Azure OpenAI or Ollama embedding
// model to the ai.embedding input of the vector store nodes. Anthropic offers
// no embeddings API and is not an option.
package aiembeddings

import (
	"context"
	"errors"
	"fmt"
	"strings"

	credazure "github.com/engi-hq/engi/internal/credtype/azureopenai"
	credollama "github.com/engi-hq/engi/internal/credtype/ollama"
	credopenai "github.com/engi-hq/engi/internal/credtype/openai"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.embeddings"

// credentialTypes maps a provider id to its credential type.
var credentialTypes = map[string]string{
	port.LLMProviderOpenAI:      credopenai.Name,
	port.LLMProviderAzureOpenAI: credazure.Name,
	port.LLMProviderOllama:      credollama.Name,
}

// Config is what the node needs from the host.
type Config struct {
	// Factory builds the embedder. Nil makes Supply fail with nodesdk.ErrUnavailable.
	Factory port.EmbedderFactory
	// DefaultModels maps a provider id to the model used when the node sets none.
	DefaultModels map[string]string
}

// Node is the Embeddings sub-node.
type Node struct{ cfg Config }

var _ nodesdk.Supplier = (*Node)(nil)

// New returns an Embeddings node.
func New(cfg Config) *Node { return &Node{cfg: cfg} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Embeddings", Group: nodesdk.GroupAI, Icon: "sparkles",
		Description: "An embedding model (OpenAI, Azure OpenAI or Ollama) that turns text into vectors. Connect it to the Embedding input of a Vector Store node or a vector search tool.",
		Outputs:     []nodesdk.PortSpec{{Name: "embedding", Kind: nodesdk.PortAIEmbedding}},
		Credentials: []nodesdk.CredentialRef{
			{Name: credopenai.Name, Type: credopenai.Name},
			{Name: credazure.Name, Type: credazure.Name},
			{Name: credollama.Name, Type: credollama.Name},
		},
		Properties: []nodesdk.Property{
			{Name: "provider", DisplayName: "Provider", Type: nodesdk.PropOptions, Default: port.LLMProviderOpenAI,
				Description: "Bind the credential of the matching type. Anthropic has no embeddings API.",
				Options: []nodesdk.Option{
					nodekit.Opt(port.LLMProviderOpenAI, "OpenAI"), nodekit.Opt(port.LLMProviderAzureOpenAI, "Azure OpenAI"),
					nodekit.Opt(port.LLMProviderOllama, "Ollama"),
				}},
			{Name: "model", DisplayName: "Model", Type: nodesdk.PropString,
				Description: "Embedding model, for example text-embedding-3-small or nomic-embed-text. Empty uses the provider default (nodes.ai.rag.*EmbeddingModel). For Azure OpenAI this is the deployment name and is required. The same model must be used to insert and to search."},
			{Name: "dimensions", DisplayName: "Dimensions", Type: nodesdk.PropNumber,
				Description: "Ask the provider for shorter vectors (OpenAI text-embedding-3, recent Ollama models). Empty uses the model default."},
		},
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("embeddings: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.Embedder.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	if n.cfg.Factory == nil {
		return nil, fmt.Errorf("embeddings: %w", nodesdk.ErrUnavailable)
	}
	provider, err := nodesdk.ParamString(ctx, in, "provider", 0)
	if err != nil {
		return nil, err
	}
	credType, ok := credentialTypes[provider]
	if !ok {
		return nil, fmt.Errorf("unknown embeddings provider %q", provider)
	}
	model, err := nodesdk.ParamString(ctx, in, "model", 0)
	if err != nil {
		return nil, err
	}
	if model = strings.TrimSpace(model); model == "" {
		model = n.cfg.DefaultModels[provider]
	}
	if model == "" {
		return nil, errors.New("model is required")
	}
	dims, err := nodesdk.ParamInt(ctx, in, "dimensions", 0)
	if err != nil {
		return nil, err
	}
	if dims < 0 {
		return nil, errors.New("dimensions must not be negative")
	}
	cred, err := in.Credential(ctx, credType)
	if err != nil {
		return nil, fmt.Errorf("loading credential %q: %w", credType, err)
	}
	e, err := n.cfg.Factory.New(provider, cred, port.EmbedderSettings{Model: model, Dimensions: dims}, in.HTTP())
	if err != nil {
		return nil, fmt.Errorf("creating %s embedder: %w", provider, err)
	}
	return e, nil
}
