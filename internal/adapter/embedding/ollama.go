package embedding

import (
	"context"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type ollamaClient struct {
	baseURL    string
	apiKey     string
	headerName string
	http       nodesdk.HTTPClient
}

type ollamaRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type ollamaResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float32 `json:"embeddings"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

func (c *ollamaClient) embedBatch(ctx context.Context, model string, dims int, texts []string) (batchResult, error) {
	req, err := llmkit.JSONRequest(ctx, strings.TrimRight(c.baseURL, "/")+"/api/embed", ollamaRequest{Model: model, Input: texts, Dimensions: dims})
	if err != nil {
		return batchResult{}, err
	}
	if c.apiKey != "" {
		if c.headerName != "" {
			req.Header.Set(c.headerName, c.apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
	}
	resp, err := llmkit.Send(ctx, c.http, port.LLMProviderOllama, req)
	if err != nil {
		return batchResult{}, err
	}
	defer resp.Body.Close()
	var out ollamaResponse
	if err := llmkit.DecodeJSON(port.LLMProviderOllama, resp.Body, &out); err != nil {
		return batchResult{}, err
	}
	return batchResult{vectors: out.Embeddings, model: out.Model, inputTokens: out.PromptEvalCount}, nil
}
