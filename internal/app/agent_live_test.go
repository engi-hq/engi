package app

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/llm/ollama"
	"github.com/engi-hq/engi/internal/node/aiagent"
	"github.com/engi-hq/engi/internal/node/aitoolcode"
	"github.com/engi-hq/engi/internal/node/code"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type liveModel struct{ llm port.LLM }

func (m liveModel) Model(context.Context) (port.LLM, port.LLMRequest, error) {
	return m.llm, port.LLMRequest{Model: os.Getenv("ENGI_TEST_OLLAMA_MODEL"), MaxTokens: 256}, nil
}

// TestLiveOllamaAgent runs the agent against a real Ollama server with a code
// tool. It runs only when ENGI_TEST_OLLAMA_URL and ENGI_TEST_OLLAMA_MODEL are
// both set (for example http://localhost:11434 and llama3.2:3b). A small model
// may not call the tool, so it only checks the loop completes and answers.
func TestLiveOllamaAgent(t *testing.T) {
	url, model := os.Getenv("ENGI_TEST_OLLAMA_URL"), os.Getenv("ENGI_TEST_OLLAMA_MODEL")
	if url == "" || model == "" {
		t.Skip("set ENGI_TEST_OLLAMA_URL and ENGI_TEST_OLLAMA_MODEL to run")
	}
	c, err := ollama.New(ollama.Config{BaseURL: url}, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	tn := aitoolcode.New(aitoolcode.Config{Limits: code.Limits{Timeout: time.Second, MaxCallStackDepth: 64, MaxOutputBytes: 1 << 10}})
	toolIn := nodetest.New(tn.Describe(), nil, map[string]any{
		"name": "add", "description": "Adds two numbers a and b and returns the sum.",
		"parameters": `{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`,
		"jsCode":     `return args.a + args.b;`,
	})
	n := aiagent.New(aiagent.Config{Clock: fixed{}, Limits: aiagent.Limits{
		MaxIterations: 4, MaxTotalTokens: 20000, Timeout: 2 * time.Minute, ToolConcurrency: 2, MaxToolResultBytes: 4096,
	}})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"prompt": "What is 17 plus 25? Use the add tool."})
	in.Subs = map[nodesdk.PortKind][]nodesdk.Supplied{nodesdk.PortAIModel: {{Node: "Model", Value: liveModel{c}}}}
	if in.Subs[nodesdk.PortAITool], err = nodetest.SupplyFrom(context.Background(), "Add", tn, toolIn); err != nil {
		t.Fatal(err)
	}
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	j := out.Ports[0][0].JSON
	if strings.TrimSpace(j["output"].(string)) == "" {
		t.Fatalf("empty answer: %v", j)
	}
	t.Logf("answer: %v, steps: %d, iterations: %v", j["output"], len(j["steps"].([]any)), j["iterations"])
}
