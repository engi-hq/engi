package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Embedder turns texts into vectors with one embedding model. An Embedder is
// bound to a provider, credential and model when it is built, so callers only
// pass texts. Implementations split large inputs into provider-sized batches
// themselves and must be safe for concurrent use.
//
// Failures are returned as *LLMError (the same type the chat adapters use, so
// callers can decide whether to retry); a bad provider or credential is an
// *LLMConfigError from the factory.
type Embedder interface {
	// Embed returns one vector per text, in order. An empty texts slice
	// returns an empty result without calling the provider.
	Embed(ctx context.Context, texts []string) (EmbedResult, error)
}

// EmbedResult is the outcome of Embedder.Embed.
type EmbedResult struct {
	// Vectors has one entry per input text, all of length Dimensions.
	Vectors [][]float32
	// Model is the model the provider reports having used (or the requested one).
	Model string
	// Dimensions is the length of every vector (0 when no text was embedded).
	Dimensions int
	// Usage is the provider-reported token usage summed over all batches.
	Usage EmbedUsage
}

// EmbedUsage is the token usage of embedding calls.
type EmbedUsage struct {
	// InputTokens is 0 when the provider reports none.
	InputTokens int
}

// EmbedderSettings selects the model of a new Embedder.
type EmbedderSettings struct {
	// Model is the embedding model, or for Azure OpenAI the deployment name.
	Model string
	// Dimensions asks the provider for shortened vectors when it supports it
	// (OpenAI text-embedding-3, Azure, recent Ollama). 0 uses the model default.
	Dimensions int
}

// EmbedderFactory builds an Embedder for one provider from a decrypted
// credential. Anthropic has no embeddings API, so only LLMProviderOpenAI,
// LLMProviderAzureOpenAI and LLMProviderOllama are accepted. Building is cheap
// and does no I/O.
type EmbedderFactory interface {
	// New fails with *LLMConfigError when the provider is unsupported or the
	// credential or settings are incomplete.
	New(provider string, cred nodesdk.Credential, set EmbedderSettings, httpc nodesdk.HTTPClient) (Embedder, error)
}

// EmbeddingMetrics is implemented by Metrics implementations that record
// embedding calls (the Prometheus adapter and NopMetrics do). It is a separate
// interface so adding it did not change Metrics; the embedding factory uses it
// when the injected Metrics also implements it.
type EmbeddingMetrics interface {
	// EmbeddingCall records one finished Embed call. provider is an LLMProvider*
	// id, model the requested model, outcome one of the LLMOutcome* values.
	EmbeddingCall(provider, model, outcome string, d time.Duration, usage EmbedUsage)
}

// EmbeddingCall implements EmbeddingMetrics.
func (NopMetrics) EmbeddingCall(string, string, string, time.Duration, EmbedUsage) {}

// TextSplitter cuts a long text into chunks for embedding. It is the value an
// ai.textSplitter sub-node supplies. Implementations are immutable and safe for
// concurrent use.
type TextSplitter interface {
	// Split returns the chunks of text in order. Empty or whitespace-only
	// input yields no chunks.
	Split(text string) []string
}
