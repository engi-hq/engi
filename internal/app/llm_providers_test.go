package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type recMetrics struct {
	port.NopMetrics
	provider, model string
}

func (m *recMetrics) LLMCall(p, mo, _ string, _ time.Duration, _ port.LLMUsage) {
	m.provider, m.model = p, mo
}

func TestFactoryBuildsEveryProvider(t *testing.T) {
	var path, query string
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query, header = r.URL.Path, r.URL.RawQuery, r.Header.Clone()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}],"content":[{"type":"text","text":"x"}],"message":{"content":"x"},"done":true}`)
	}))
	defer srv.Close()

	tests := []struct {
		provider string
		data     map[string]any
		wantPath string
		wantHdr  [2]string
	}{
		{port.LLMProviderAnthropic, map[string]any{"apiKey": "k", "baseUrl": srv.URL}, "/v1/messages", [2]string{"x-api-key", "k"}},
		{port.LLMProviderOpenAI, map[string]any{"apiKey": "k", "baseUrl": srv.URL + "/v1"}, "/v1/chat/completions", [2]string{"Authorization", "Bearer k"}},
		{port.LLMProviderAzureOpenAI, map[string]any{"apiKey": "k", "endpoint": srv.URL}, "/openai/deployments/dep/chat/completions", [2]string{"api-key", "k"}},
		{port.LLMProviderOllama, map[string]any{"baseUrl": srv.URL, "apiKey": "k"}, "/api/chat", [2]string{"Authorization", "Bearer k"}},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			m := &recMetrics{}
			f := newProviderFactory(config.Default().Nodes.AI, m, time.Now)
			llm, err := f.New(tt.provider, nodesdk.Credential{Data: tt.data}, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := llm.Chat(context.Background(), port.LLMRequest{Model: "dep"}); err != nil {
				t.Fatal(err)
			}
			if path != tt.wantPath || header.Get(tt.wantHdr[0]) != tt.wantHdr[1] {
				t.Fatalf("path %q header %v", path, header)
			}
			if m.provider != tt.provider || m.model != "dep" {
				t.Fatalf("metrics saw %q/%q", m.provider, m.model)
			}
		})
	}
	if query != "" && path == "/openai/deployments/dep/chat/completions" && query != "api-version="+config.Default().Nodes.AI.AzureAPIVersion {
		t.Fatalf("azure query = %q", query)
	}
}

func TestFactoryDefaultsComeFromConfig(t *testing.T) {
	cfg := config.Default().Nodes.AI
	var host string
	doer := doerFunc(func(r *http.Request) (*http.Response, error) {
		host = r.URL.Scheme + "://" + r.URL.Host
		return nil, errors.New("stop")
	})
	f := newProviderFactory(cfg, nil, time.Now)
	for provider, want := range map[string]string{
		port.LLMProviderAnthropic: cfg.AnthropicBaseURL, port.LLMProviderOpenAI: cfg.OpenAIBaseURL, port.LLMProviderOllama: cfg.OllamaBaseURL,
	} {
		llm, err := f.New(provider, nodesdk.Credential{Data: map[string]any{"apiKey": "k"}}, doer)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = llm.Chat(context.Background(), port.LLMRequest{Model: "m"})
		if host == "" || want[:len(host)] != host {
			t.Errorf("%s: called %q, want prefix of %q", provider, host, want)
		}
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestFactoryConfigErrors(t *testing.T) {
	f := newProviderFactory(config.Default().Nodes.AI, nil, time.Now)
	for name, tc := range map[string]struct {
		provider string
		data     map[string]any
	}{
		"unknown provider":   {"nope", nil},
		"anthropic no key":   {port.LLMProviderAnthropic, map[string]any{}},
		"openai no key":      {port.LLMProviderOpenAI, map[string]any{"baseUrl": "x"}},
		"azure no endpoint":  {port.LLMProviderAzureOpenAI, map[string]any{"apiKey": "k"}},
		"azure no key":       {port.LLMProviderAzureOpenAI, map[string]any{"endpoint": "https://x"}},
		"anthropic bad type": {port.LLMProviderAnthropic, map[string]any{"apiKey": 42}},
	} {
		_, err := f.New(tc.provider, nodesdk.Credential{Data: tc.data}, http.DefaultClient)
		var ce *port.LLMConfigError
		if !errors.As(err, &ce) {
			t.Errorf("%s: err = %v, want *LLMConfigError", name, err)
		}
	}
}
