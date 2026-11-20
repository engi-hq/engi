package embedding

import (
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/config"
	credazure "github.com/engi-hq/engi/internal/credtype/azureopenai"
	credollama "github.com/engi-hq/engi/internal/credtype/ollama"
	credopenai "github.com/engi-hq/engi/internal/credtype/openai"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Factory implements port.EmbedderFactory.
type Factory struct {
	cfg     config.AINodeConfig
	metrics port.EmbeddingMetrics
	now     func() time.Time
}

var _ port.EmbedderFactory = (*Factory)(nil)

// New returns a Factory using the nodes.ai settings. metrics may be nil or a
// port.Metrics that does not implement port.EmbeddingMetrics (nothing is
// recorded then). now is the clock the metrics decorator times calls with.
func New(cfg config.AINodeConfig, metrics port.Metrics, now func() time.Time) *Factory {
	em, ok := metrics.(port.EmbeddingMetrics)
	if !ok {
		em = port.NopMetrics{}
	}
	return &Factory{cfg: cfg, metrics: em, now: now}
}

// New implements port.EmbedderFactory.
func (f *Factory) New(provider string, cred nodesdk.Credential, set port.EmbedderSettings, httpc nodesdk.HTTPClient) (port.Embedder, error) {
	bad := func(msg string) (port.Embedder, error) {
		return nil, &port.LLMConfigError{Provider: provider, Message: msg}
	}
	set.Model = strings.TrimSpace(set.Model)
	if set.Model == "" {
		return bad("embedding model is required")
	}
	if set.Dimensions < 0 {
		return bad("dimensions must not be negative")
	}
	data := cred.Data
	var client batchClient
	switch provider {
	case port.LLMProviderOpenAI:
		if field(data, credopenai.FieldAPIKey) == "" {
			return bad("API key is required")
		}
		client = &openAIClient{provider: provider, http: httpc, cfg: openAIConfig{
			apiKey: field(data, credopenai.FieldAPIKey), baseURL: orDefault(field(data, credopenai.FieldBaseURL), f.cfg.OpenAIBaseURL),
			organization: field(data, credopenai.FieldOrganization),
		}}
	case port.LLMProviderAzureOpenAI:
		if field(data, credazure.FieldEndpoint) == "" {
			return bad("resource endpoint is required")
		}
		if field(data, credazure.FieldAPIKey) == "" {
			return bad("API key is required")
		}
		client = &openAIClient{provider: provider, http: httpc, cfg: openAIConfig{
			apiKey: field(data, credazure.FieldAPIKey), azureEndpoint: field(data, credazure.FieldEndpoint),
			apiVersion: orDefault(field(data, credazure.FieldAPIVersion), f.cfg.AzureAPIVersion),
		}}
	case port.LLMProviderOllama:
		client = &ollamaClient{
			baseURL: orDefault(field(data, credollama.FieldBaseURL), f.cfg.OllamaBaseURL), http: httpc,
			apiKey: field(data, credollama.FieldAPIKey), headerName: field(data, credollama.FieldHeaderName),
		}
	case port.LLMProviderAnthropic:
		return bad("Anthropic has no embeddings API; use OpenAI, Azure OpenAI or Ollama")
	default:
		return bad("unknown provider")
	}
	e := &embedder{client: client, provider: provider, set: set, size: f.cfg.RAG.EmbeddingBatchSize, timeout: f.cfg.RAG.EmbeddingRequestTimeout}
	return &metered{inner: e, provider: provider, model: set.Model, metrics: f.metrics, now: f.now}, nil
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
