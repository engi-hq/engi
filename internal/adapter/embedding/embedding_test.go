package embedding_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/embedding"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type recMetrics struct {
	port.NopMetrics
	mu    sync.Mutex
	calls []string
	usage port.EmbedUsage
}

func (m *recMetrics) EmbeddingCall(p, mo, o string, _ time.Duration, u port.EmbedUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, p+"/"+mo+"/"+o)
	m.usage = u
}

func factory(m port.Metrics, batch int) *embedding.Factory {
	cfg := config.Default().Nodes.AI
	cfg.RAG.EmbeddingBatchSize = batch
	return embedding.New(cfg, m, time.Now)
}

func cred(typ string, data map[string]any) nodesdk.Credential {
	return nodesdk.Credential{Type: typ, Data: data}
}

// openAIServer answers /embeddings with a vector [len(text), index] per text,
// in reverse order to prove the client sorts by index.
func openAIServer(t *testing.T, seen *[]map[string]any, hdr *http.Header, path *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		*seen = append(*seen, req)
		*hdr = r.Header.Clone()
		*path = r.URL.RequestURI()
		in := req["input"].([]any)
		var data []map[string]any
		for i := len(in) - 1; i >= 0; i-- {
			data = append(data, map[string]any{"index": i, "embedding": []float64{float64(len(in[i].(string))), float64(i)}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "text-embedding-3-small", "data": data, "usage": map[string]any{"prompt_tokens": len(in), "total_tokens": len(in)}})
	}))
}

func TestOpenAIBatchesAndOrders(t *testing.T) {
	var seen []map[string]any
	var hdr http.Header
	var path string
	srv := openAIServer(t, &seen, &hdr, &path)
	defer srv.Close()
	m := &recMetrics{}
	e, err := factory(m, 2).New(port.LLMProviderOpenAI, cred("engi.openai", map[string]any{"apiKey": "k", "baseUrl": srv.URL + "/v1", "organization": "org"}),
		port.EmbedderSettings{Model: "text-embedding-3-small", Dimensions: 2}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Embed(context.Background(), []string{"a", "bb", "ccc", "dddd", "eeeee"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || len(seen[0]["input"].([]any)) != 2 || len(seen[2]["input"].([]any)) != 1 {
		t.Fatalf("batches: %v", seen)
	}
	if seen[0]["dimensions"] != float64(2) || seen[0]["model"] != "text-embedding-3-small" {
		t.Fatalf("body %v", seen[0])
	}
	if len(res.Vectors) != 5 || res.Dimensions != 2 || res.Vectors[0][0] != 1 || res.Vectors[1][0] != 2 || res.Vectors[4][0] != 5 {
		t.Fatalf("vectors %v", res)
	}
	if res.Usage.InputTokens != 5 || res.Model != "text-embedding-3-small" {
		t.Fatalf("usage/model %+v", res)
	}
	if path != "/v1/embeddings" || hdr.Get("Authorization") != "Bearer k" || hdr.Get("OpenAI-Organization") != "org" {
		t.Fatalf("request %s %v", path, hdr)
	}
	if len(m.calls) != 1 || m.calls[0] != "openai/text-embedding-3-small/ok" {
		t.Fatalf("metrics %v", m.calls)
	}
}

func TestAzureRequest(t *testing.T) {
	var seen []map[string]any
	var hdr http.Header
	var path string
	srv := openAIServer(t, &seen, &hdr, &path)
	defer srv.Close()
	e, err := factory(nil, 10).New(port.LLMProviderAzureOpenAI, cred("engi.azureOpenAI", map[string]any{"apiKey": "az", "endpoint": srv.URL, "apiVersion": "2024-02-01"}),
		port.EmbedderSettings{Model: "my-deploy"}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatal(err)
	}
	if path != "/openai/deployments/my-deploy/embeddings?api-version=2024-02-01" || hdr.Get("api-key") != "az" || hdr.Get("Authorization") != "" {
		t.Fatalf("request %s %v", path, hdr)
	}
}

func TestOllama(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("X-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "nomic-embed-text", "embeddings": [][]float64{{1, 2, 3}, {4, 5, 6}}, "prompt_eval_count": 7})
	}))
	defer srv.Close()
	e, err := factory(nil, 10).New(port.LLMProviderOllama, cred("engi.ollama", map[string]any{"baseUrl": srv.URL, "apiKey": "s", "headerName": "X-Key"}),
		port.EmbedderSettings{Model: "nomic-embed-text"}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Embed(context.Background(), []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dimensions != 3 || res.Usage.InputTokens != 7 || res.Vectors[1][2] != 6 || auth != "s" {
		t.Fatalf("%+v auth=%q", res, auth)
	}
	if _, has := got["dimensions"]; has {
		t.Fatalf("dimensions sent when unset: %v", got)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    func(*port.LLMError) bool
		outcome string
	}{
		{"rate limit", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		}, func(e *port.LLMError) bool {
			return e.Status == 429 && e.Retryable && e.RetryAfter == 3*time.Second && e.Message == "slow down"
		}, "rate_limited"},
		{"bad request", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
		}, func(e *port.LLMError) bool { return e.Status == 400 && !e.Retryable }, "error"},
		{"wrong count", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}, func(e *port.LLMError) bool { return strings.Contains(e.Message, "0 vectors for 1 texts") }, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			m := &recMetrics{}
			e, _ := factory(m, 10).New(port.LLMProviderOpenAI, cred("engi.openai", map[string]any{"apiKey": "k", "baseUrl": srv.URL}),
				port.EmbedderSettings{Model: "m"}, http.DefaultClient)
			_, err := e.Embed(context.Background(), []string{"a"})
			var le *port.LLMError
			if !errors.As(err, &le) || !tt.want(le) {
				t.Fatalf("err = %v", err)
			}
			if len(m.calls) != 1 || !strings.HasSuffix(m.calls[0], "/"+tt.outcome) {
				t.Fatalf("metrics %v", m.calls)
			}
		})
	}
}

func TestEmptyInputAndEmptyText(t *testing.T) {
	e, _ := factory(nil, 10).New(port.LLMProviderOllama, cred("engi.ollama", map[string]any{}), port.EmbedderSettings{Model: "m"}, http.DefaultClient)
	res, err := e.Embed(context.Background(), nil)
	if err != nil || len(res.Vectors) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := e.Embed(context.Background(), []string{"ok", ""}); err == nil || !strings.Contains(err.Error(), "text 1 is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestMixedDimensionsRejected(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{make([]float64, n+1)}})
	}))
	defer srv.Close()
	e, _ := factory(nil, 1).New(port.LLMProviderOllama, cred("engi.ollama", map[string]any{"baseUrl": srv.URL}), port.EmbedderSettings{Model: "m"}, http.DefaultClient)
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("err = %v", err)
	}
}

func TestFactoryConfigErrors(t *testing.T) {
	f := factory(nil, 10)
	for name, tt := range map[string]struct {
		provider string
		cred     map[string]any
		set      port.EmbedderSettings
		msg      string
	}{
		"anthropic":     {port.LLMProviderAnthropic, map[string]any{"apiKey": "k"}, port.EmbedderSettings{Model: "m"}, "no embeddings API"},
		"unknown":       {"bogus", nil, port.EmbedderSettings{Model: "m"}, "unknown provider"},
		"no model":      {port.LLMProviderOllama, nil, port.EmbedderSettings{}, "model is required"},
		"neg dims":      {port.LLMProviderOllama, nil, port.EmbedderSettings{Model: "m", Dimensions: -1}, "dimensions"},
		"openai no key": {port.LLMProviderOpenAI, nil, port.EmbedderSettings{Model: "m"}, "API key"},
		"azure no end":  {port.LLMProviderAzureOpenAI, map[string]any{"apiKey": "k"}, port.EmbedderSettings{Model: "m"}, "endpoint"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.New(tt.provider, nodesdk.Credential{Data: tt.cred}, tt.set, http.DefaultClient)
			var ce *port.LLMConfigError
			if !errors.As(err, &ce) || !strings.Contains(ce.Message, tt.msg) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
