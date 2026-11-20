package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// AINodeConfig tunes the AI nodes and the model providers behind them. Base
// URLs and API versions are defaults: a credential may override the base URL.
type AINodeConfig struct {
	// AnthropicBaseURL is the Anthropic API root.
	AnthropicBaseURL string `yaml:"anthropicBaseUrl"`
	// AnthropicVersion is sent in the anthropic-version header.
	AnthropicVersion string `yaml:"anthropicVersion"`
	// OpenAIBaseURL is the OpenAI API root (including /v1).
	OpenAIBaseURL string `yaml:"openaiBaseUrl"`
	// OllamaBaseURL is the Ollama server root.
	OllamaBaseURL string `yaml:"ollamaBaseUrl"`
	// AzureAPIVersion is the api-version query value for Azure OpenAI when the
	// credential does not set one.
	AzureAPIVersion string `yaml:"azureApiVersion"`
	// AnthropicDefaultModel, OpenAIDefaultModel and OllamaDefaultModel apply
	// when a node leaves the model empty (Azure always needs a deployment name).
	AnthropicDefaultModel string `yaml:"anthropicDefaultModel"`
	OpenAIDefaultModel    string `yaml:"openaiDefaultModel"`
	OllamaDefaultModel    string `yaml:"ollamaDefaultModel"`
	// DefaultMaxTokens caps a reply when a node sets no limit (Anthropic
	// requires one on every request).
	DefaultMaxTokens int `yaml:"defaultMaxTokens"`
	// RequestTimeout bounds one model call, streaming included.
	RequestTimeout time.Duration `yaml:"requestTimeout"`
	// Agent bounds the AI Agent node.
	Agent AIAgentConfig `yaml:"agent"`
	// Memory bounds chat memory.
	Memory AIMemoryConfig `yaml:"memory"`
	// Tool bounds the tool sub-nodes.
	Tool AIToolConfig `yaml:"tool"`
	// RAG tunes embeddings and the vector store.
	RAG AIRAGConfig `yaml:"rag"`
}

func defaultAI() AINodeConfig {
	return AINodeConfig{
		AnthropicBaseURL: "https://api.anthropic.com", AnthropicVersion: "2023-06-01",
		OpenAIBaseURL: "https://api.openai.com/v1", OllamaBaseURL: "http://localhost:11434",
		AzureAPIVersion:       "2024-10-21",
		AnthropicDefaultModel: "claude-sonnet-5-5", OpenAIDefaultModel: "gpt-4o-mini", OllamaDefaultModel: "llama3.2:3b",
		DefaultMaxTokens: 4096, RequestTimeout: 2 * time.Minute,
		Agent: defaultAIAgent(), Memory: defaultAIMemory(), Tool: defaultAITool(), RAG: defaultAIRAG(),
	}
}

// Validate reports every problem with the AI node settings.
func (c AINodeConfig) Validate() error {
	var errs []error
	for name, v := range map[string]string{
		"nodes.ai.anthropicBaseUrl": c.AnthropicBaseURL, "nodes.ai.openaiBaseUrl": c.OpenAIBaseURL, "nodes.ai.ollamaBaseUrl": c.OllamaBaseURL,
	} {
		if u, err := url.Parse(v); err != nil || !u.IsAbs() || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s %q is not an absolute URL", name, v))
		}
	}
	for name, v := range map[string]string{
		"nodes.ai.anthropicVersion": c.AnthropicVersion, "nodes.ai.azureApiVersion": c.AzureAPIVersion,
		"nodes.ai.anthropicDefaultModel": c.AnthropicDefaultModel, "nodes.ai.openaiDefaultModel": c.OpenAIDefaultModel,
		"nodes.ai.ollamaDefaultModel": c.OllamaDefaultModel,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, errors.New(name+" must not be empty"))
		}
	}
	errs = appendPositive(errs, "nodes.ai.defaultMaxTokens", int64(c.DefaultMaxTokens))
	errs = appendPositive(errs, "nodes.ai.requestTimeout", int64(c.RequestTimeout))
	errs = c.Agent.validate(errs)
	errs = c.Memory.validate(errs)
	errs = c.Tool.validate(errs)
	errs = c.RAG.validate(errs)
	return errors.Join(errs...)
}

func aiSettings() []setting {
	return slices.Concat(aiModelSettings(), aiAgentSettings(), aiRAGSettings())
}

func aiModelSettings() []setting {
	return []setting{
		{"ENGI_NODES_AI_ANTHROPIC_BASE_URL", "nodes-ai-anthropic-base-url", "Anthropic API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.AnthropicBaseURL })},
		{"ENGI_NODES_AI_ANTHROPIC_VERSION", "nodes-ai-anthropic-version", "anthropic-version header value",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.AnthropicVersion })},
		{"ENGI_NODES_AI_OPENAI_BASE_URL", "nodes-ai-openai-base-url", "OpenAI API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.OpenAIBaseURL })},
		{"ENGI_NODES_AI_OLLAMA_BASE_URL", "nodes-ai-ollama-base-url", "Ollama server base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.OllamaBaseURL })},
		{"ENGI_NODES_AI_AZURE_API_VERSION", "nodes-ai-azure-api-version", "default Azure OpenAI api-version",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.AzureAPIVersion })},
		{"ENGI_NODES_AI_ANTHROPIC_DEFAULT_MODEL", "nodes-ai-anthropic-default-model", "Anthropic model used when a node sets none",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.AnthropicDefaultModel })},
		{"ENGI_NODES_AI_OPENAI_DEFAULT_MODEL", "nodes-ai-openai-default-model", "OpenAI model used when a node sets none",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.OpenAIDefaultModel })},
		{"ENGI_NODES_AI_OLLAMA_DEFAULT_MODEL", "nodes-ai-ollama-default-model", "Ollama model used when a node sets none",
			stringSetting(func(c *Config) *string { return &c.Nodes.AI.OllamaDefaultModel })},
		{"ENGI_NODES_AI_DEFAULT_MAX_TOKENS", "nodes-ai-default-max-tokens", "reply token cap when a node sets none",
			intSetting(func(c *Config) *int { return &c.Nodes.AI.DefaultMaxTokens })},
		{"ENGI_NODES_AI_REQUEST_TIMEOUT", "nodes-ai-request-timeout", "time limit of one model call",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.AI.RequestTimeout })},
	}
}
