package embedding

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/engi-hq/engi/internal/llmkit"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// openAIConfig configures the OpenAI and Azure OpenAI embeddings client. Set
// azureEndpoint and apiVersion for Azure, baseURL for OpenAI.
type openAIConfig struct {
	apiKey        string
	baseURL       string
	organization  string
	azureEndpoint string
	apiVersion    string
}

type openAIClient struct {
	cfg      openAIConfig
	http     nodesdk.HTTPClient
	provider string
}

func (c *openAIClient) endpoint(model string) string {
	if c.provider == port.LLMProviderAzureOpenAI {
		return strings.TrimRight(c.cfg.azureEndpoint, "/") + "/openai/deployments/" + url.PathEscape(model) +
			"/embeddings?api-version=" + url.QueryEscape(c.cfg.apiVersion)
	}
	return strings.TrimRight(c.cfg.baseURL, "/") + "/embeddings"
}

type openAIRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions,omitempty"`
	EncodingFormat string   `json:"encoding_format"`
}

type openAIResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// encodingFloat asks for plain JSON number arrays (the API default).
const encodingFloat = "float"

func (c *openAIClient) embedBatch(ctx context.Context, model string, dims int, texts []string) (batchResult, error) {
	req, err := llmkit.JSONRequest(ctx, c.endpoint(model), openAIRequest{Model: model, Input: texts, Dimensions: dims, EncodingFormat: encodingFloat})
	if err != nil {
		return batchResult{}, err
	}
	if c.provider == port.LLMProviderAzureOpenAI {
		req.Header.Set("api-key", c.cfg.apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.cfg.apiKey)
		if c.cfg.organization != "" {
			req.Header.Set("OpenAI-Organization", c.cfg.organization)
		}
	}
	resp, err := llmkit.Send(ctx, c.http, c.provider, req)
	if err != nil {
		return batchResult{}, err
	}
	defer resp.Body.Close()
	var out openAIResponse
	if err := llmkit.DecodeJSON(c.provider, resp.Body, &out); err != nil {
		return batchResult{}, err
	}
	sort.SliceStable(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	res := batchResult{model: out.Model, inputTokens: out.Usage.PromptTokens}
	if res.inputTokens == 0 {
		res.inputTokens = out.Usage.TotalTokens
	}
	for _, d := range out.Data {
		res.vectors = append(res.vectors, d.Embedding)
	}
	return res, nil
}
