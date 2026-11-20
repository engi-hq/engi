// Package embedding implements port.EmbedderFactory over the OpenAI and Azure
// OpenAI embeddings API and Ollama's /api/embed. Anthropic has no embeddings
// API, so asking for it is a configuration error.
//
// Every Embedder splits its input into batches of Config.BatchSize texts, sends
// them one after another (never concurrently), bounds each request with
// Config.RequestTimeout, and records the whole call in the embedding metrics.
// The HTTP helpers (error mapping, retry hints) come from the llm adapters'
// shared kit, llmkit, so failures are *port.LLMError like chat failures.
package embedding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/port"
)

// batchResult is what one provider request returns.
type batchResult struct {
	vectors     [][]float32
	model       string
	inputTokens int
}

// batchClient embeds one provider-sized batch.
type batchClient interface {
	embedBatch(ctx context.Context, model string, dims int, texts []string) (batchResult, error)
}

// embedder implements port.Embedder on top of a batchClient.
type embedder struct {
	client   batchClient
	provider string
	set      port.EmbedderSettings
	size     int
	timeout  time.Duration
}

// Embed implements port.Embedder.
func (e *embedder) Embed(ctx context.Context, texts []string) (port.EmbedResult, error) {
	res := port.EmbedResult{Model: e.set.Model}
	for i, t := range texts {
		if t == "" {
			return res, fmt.Errorf("text %d is empty", i)
		}
	}
	for start := 0; start < len(texts); start += e.size {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("embedding with %s: %w", e.provider, err)
		}
		end := min(start+e.size, len(texts))
		br, err := e.batch(ctx, texts[start:end])
		if err != nil {
			return res, err
		}
		for _, v := range br.vectors {
			if res.Dimensions == 0 {
				res.Dimensions = len(v)
			}
			if len(v) != res.Dimensions {
				return res, &port.LLMError{Provider: e.provider, Message: fmt.Sprintf("provider returned vectors of %d and %d dimensions", res.Dimensions, len(v))}
			}
		}
		res.Vectors = append(res.Vectors, br.vectors...)
		res.Usage.InputTokens += br.inputTokens
		if br.model != "" {
			res.Model = br.model
		}
	}
	return res, nil
}

func (e *embedder) batch(ctx context.Context, texts []string) (batchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	br, err := e.client.embedBatch(ctx, e.set.Model, e.set.Dimensions, texts)
	if err != nil {
		return br, err
	}
	if len(br.vectors) != len(texts) {
		return br, &port.LLMError{Provider: e.provider, Message: fmt.Sprintf("provider returned %d vectors for %d texts", len(br.vectors), len(texts))}
	}
	return br, nil
}

// metered records each Embed call of inner.
type metered struct {
	inner    port.Embedder
	provider string
	model    string
	metrics  port.EmbeddingMetrics
	now      func() time.Time
}

// Embed implements port.Embedder.
func (m *metered) Embed(ctx context.Context, texts []string) (port.EmbedResult, error) {
	start := m.now()
	res, err := m.inner.Embed(ctx, texts)
	outcome := port.LLMOutcomeOK
	if err != nil {
		outcome = port.LLMOutcomeError
		var le *port.LLMError
		if errors.As(err, &le) && le.Status == 429 {
			outcome = port.LLMOutcomeRateLimited
		}
	}
	if len(texts) > 0 {
		m.metrics.EmbeddingCall(m.provider, m.model, outcome, m.now().Sub(start), res.Usage)
	}
	return res, err
}
