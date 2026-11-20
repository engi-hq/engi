package app

import (
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/adapter/llm/anthropic"
	"github.com/engi-hq/engi/internal/adapter/llm/ollama"
	"github.com/engi-hq/engi/internal/adapter/llm/openai"
	"github.com/engi-hq/engi/internal/config"
	credanthropic "github.com/engi-hq/engi/internal/credtype/anthropic"
	credazure "github.com/engi-hq/engi/internal/credtype/azureopenai"
	credollama "github.com/engi-hq/engi/internal/credtype/ollama"
	credopenai "github.com/engi-hq/engi/internal/credtype/openai"
	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// providerFactory implements port.LLMFactory: it builds the model client for a
// provider id from a decrypted credential and wraps it with the request timeout
// and the metrics decorator. It maps provider ids to concrete LLM adapters, which
// is composition work, so it lives in the composition root rather than in an adapter.
type providerFactory struct {
	cfg     config.AINodeConfig
	metrics port.Metrics
	now     func() time.Time
}

var _ port.LLMFactory = (*providerFactory)(nil)

// newProviderFactory returns a providerFactory using the nodes.ai settings. now is
// the clock the metrics decorator times calls with.
func newProviderFactory(cfg config.AINodeConfig, metrics port.Metrics, now func() time.Time) *providerFactory {
	if metrics == nil {
		metrics = port.NopMetrics{}
	}
	return &providerFactory{cfg: cfg, metrics: metrics, now: now}
}

// New implements port.LLMFactory.
func (f *providerFactory) New(provider string, cred nodesdk.Credential, httpc nodesdk.HTTPClient) (port.LLM, error) {
	llm, err := f.build(provider, cred.Data, httpc)
	if err != nil {
		return nil, err
	}
	return llmkit.WithMetrics(llmkit.WithTimeout(llm, f.cfg.RequestTimeout), provider, f.metrics, f.now), nil
}

func (f *providerFactory) build(provider string, data map[string]any, httpc nodesdk.HTTPClient) (port.LLM, error) {
	bad := func(msg string) (port.LLM, error) { return nil, &port.LLMConfigError{Provider: provider, Message: msg} }
	switch provider {
	case port.LLMProviderAnthropic:
		c, err := anthropic.New(anthropic.Config{
			APIKey: field(data, credanthropic.FieldAPIKey), BaseURL: orDefault(field(data, credanthropic.FieldBaseURL), f.cfg.AnthropicBaseURL),
			Version: f.cfg.AnthropicVersion, DefaultMaxTokens: f.cfg.DefaultMaxTokens,
		}, httpc)
		return wrap(c, err, bad)
	case port.LLMProviderOpenAI:
		c, err := openai.New(openai.Config{
			APIKey: field(data, credopenai.FieldAPIKey), BaseURL: orDefault(field(data, credopenai.FieldBaseURL), f.cfg.OpenAIBaseURL),
			Organization: field(data, credopenai.FieldOrganization),
		}, httpc)
		return wrap(c, err, bad)
	case port.LLMProviderAzureOpenAI:
		endpoint := field(data, credazure.FieldEndpoint)
		if endpoint == "" {
			return bad("resource endpoint is required")
		}
		c, err := openai.New(openai.Config{
			APIKey: field(data, credazure.FieldAPIKey), AzureEndpoint: endpoint,
			APIVersion: orDefault(field(data, credazure.FieldAPIVersion), f.cfg.AzureAPIVersion),
		}, httpc)
		return wrap(c, err, bad)
	case port.LLMProviderOllama:
		c, err := ollama.New(ollama.Config{
			BaseURL: orDefault(field(data, credollama.FieldBaseURL), f.cfg.OllamaBaseURL),
			APIKey:  field(data, credollama.FieldAPIKey), HeaderName: field(data, credollama.FieldHeaderName),
		}, httpc)
		return wrap(c, err, bad)
	}
	return bad("unknown provider")
}

func wrap[T port.LLM](c T, err error, bad func(string) (port.LLM, error)) (port.LLM, error) {
	if err != nil {
		return bad(err.Error())
	}
	return c, nil
}

func field(data map[string]any, name string) string {
	s, _ := data[name].(string)
	return strings.TrimSpace(s)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
