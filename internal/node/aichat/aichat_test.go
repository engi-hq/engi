package aichat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aichat"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeLLM struct {
	reqs  []port.LLMRequest
	reply func(i int, req port.LLMRequest) (port.LLMResponse, error)
}

func (f *fakeLLM) Chat(_ context.Context, req port.LLMRequest) (port.LLMResponse, error) {
	f.reqs = append(f.reqs, req)
	return f.reply(len(f.reqs)-1, req)
}

func (f *fakeLLM) Stream(ctx context.Context, req port.LLMRequest, _ func(port.StreamEvent) error) (port.LLMResponse, error) {
	return f.Chat(ctx, req)
}

type fakeFactory struct {
	llm      port.LLM
	err      error
	provider string
	cred     nodesdk.Credential
}

func (f *fakeFactory) New(provider string, cred nodesdk.Credential, _ nodesdk.HTTPClient) (port.LLM, error) {
	f.provider, f.cred = provider, cred
	return f.llm, f.err
}

var defaults = map[string]string{port.LLMProviderAnthropic: "claude-sonnet-5-5", port.LLMProviderOllama: "llama3.2:3b"}

func okReply(text string) func(int, port.LLMRequest) (port.LLMResponse, error) {
	return func(int, port.LLMRequest) (port.LLMResponse, error) {
		return port.LLMResponse{Content: text, Model: "m-1", StopReason: port.StopEnd, Usage: port.LLMUsage{InputTokens: 3, OutputTokens: 4}}, nil
	}
}

func setup(llm *fakeLLM, params map[string]any, items ...map[string]any) (*aichat.Node, *fakeFactory, *nodetest.Input) {
	ff := &fakeFactory{llm: llm}
	n := aichat.New(aichat.Config{Factory: ff, DefaultModels: defaults})
	in := nodetest.New(n.Describe(), nodetest.Items(items...), params)
	in.Credentials = map[string]nodesdk.Credential{
		"engi.anthropic": {Type: "engi.anthropic", Data: map[string]any{"apiKey": "k"}},
		"engi.ollama":    {Type: "engi.ollama", Data: map[string]any{}},
	}
	return n, ff, in
}

func TestDescriptorIsValid(t *testing.T) {
	d := aichat.New(aichat.Config{}).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Group != nodesdk.GroupAI || d.Type != "engi.ai.chat" || len(d.Credentials) != 4 {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestTextReplyPerItem(t *testing.T) {
	llm := &fakeLLM{reply: okReply("pong")}
	n, ff, in := setup(llm, map[string]any{"message": "ping", "systemPrompt": "sys", "temperature": 0.3, "maxTokens": float64(77),
		"options": map[string]any{"topP": 0.8, "stopSequences": "A, B"}}, map[string]any{"a": 1}, map[string]any{"a": 2})
	out := nodetest.Run(t, n, in)
	items := out.Ports[0]
	if len(items) != 2 || len(llm.reqs) != 2 {
		t.Fatalf("items %d reqs %d", len(items), len(llm.reqs))
	}
	j := items[0].JSON
	if j["text"] != "pong" || j["model"] != "m-1" || j["stopReason"] != "end" {
		t.Fatalf("json = %v", j)
	}
	if _, has := j["json"]; has {
		t.Fatal("text format must not set json")
	}
	u := j["usage"].(map[string]any)
	if u["inputTokens"] != float64(3) || u["outputTokens"] != float64(4) || u["totalTokens"] != float64(7) {
		t.Fatalf("usage = %v", u)
	}
	if items[1].PairedItem[0].Item != 1 {
		t.Fatalf("pairing = %v", items[1].PairedItem)
	}
	r := llm.reqs[0]
	if r.Model != "claude-sonnet-5-5" || r.System != "sys" || r.MaxTokens != 77 || *r.Temperature != 0.3 || *r.TopP != 0.8 ||
		strings.Join(r.Stop, "|") != "A|B" || r.Messages[0].Content != "ping" || r.Format.Kind != port.ResponseText {
		t.Fatalf("req = %+v", r)
	}
	if ff.provider != "anthropic" || ff.cred.Data["apiKey"] != "k" {
		t.Fatalf("factory got %q %v", ff.provider, ff.cred)
	}
}

func TestExpressionsAndOptionalFieldsPerItem(t *testing.T) {
	llm := &fakeLLM{reply: okReply("x")}
	n, _, in := setup(llm, map[string]any{"message": "unused"}, map[string]any{"q": "one"}, map[string]any{"q": "two"})
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "message" {
			return []string{"first", "second"}[i], true
		}
		return nil, false
	}
	nodetest.Run(t, n, in)
	if llm.reqs[0].Messages[0].Content != "first" || llm.reqs[1].Messages[0].Content != "second" {
		t.Fatalf("reqs = %+v", llm.reqs)
	}
	if llm.reqs[0].Temperature != nil || llm.reqs[0].TopP != nil || llm.reqs[0].MaxTokens != 0 {
		t.Fatalf("unset options must stay unset: %+v", llm.reqs[0])
	}
}

func TestProviderSelectsCredentialAndDefaultModel(t *testing.T) {
	llm := &fakeLLM{reply: okReply("x")}
	n, ff, in := setup(llm, map[string]any{"provider": "ollama", "message": "m"}, map[string]any{})
	nodetest.Run(t, n, in)
	if ff.provider != "ollama" || llm.reqs[0].Model != "llama3.2:3b" {
		t.Fatalf("provider %q model %q", ff.provider, llm.reqs[0].Model)
	}
	_, _, in = setup(llm, map[string]any{"provider": "ollama", "message": "m", "model": " custom "}, map[string]any{})
	nodetest.Run(t, n, in)
	if llm.reqs[1].Model != "custom" {
		t.Fatalf("model = %q", llm.reqs[1].Model)
	}
}

func TestJSONFormats(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}
	tests := []struct {
		name   string
		params map[string]any
		reply  port.LLMResponse
		want   any
		check  func(t *testing.T, r port.LLMRequest)
		errHas string
	}{
		{"json", map[string]any{"responseFormat": "json"}, port.LLMResponse{Content: `{"n":1}`}, map[string]any{"n": float64(1)},
			func(t *testing.T, r port.LLMRequest) {
				if r.Format.Kind != port.ResponseJSON {
					t.Fatal(r.Format)
				}
			}, ""},
		{"fenced json", map[string]any{"responseFormat": "json"}, port.LLMResponse{Content: "```json\n[1,2]\n```"}, []any{float64(1), float64(2)}, nil, ""},
		{"schema object", map[string]any{"responseFormat": "jsonSchema", "schemaName": "num", "schema": schema}, port.LLMResponse{Content: `{"n":2}`}, map[string]any{"n": float64(2)},
			func(t *testing.T, r port.LLMRequest) {
				var got map[string]any
				if err := json.Unmarshal(r.Format.Schema, &got); err != nil || got["type"] != "object" || r.Format.Name != "num" || r.Format.Kind != port.ResponseJSONSchema {
					t.Fatalf("format = %+v", r.Format)
				}
			}, ""},
		{"schema string and default name", map[string]any{"responseFormat": "jsonSchema", "schema": `{"type":"object"}`}, port.LLMResponse{Content: `{}`}, map[string]any{},
			func(t *testing.T, r port.LLMRequest) {
				if r.Format.Name != "response" {
					t.Fatal(r.Format.Name)
				}
			}, ""},
		{"invalid reply", map[string]any{"responseFormat": "json"}, port.LLMResponse{Content: "sure! here you go"}, nil, nil, "not valid JSON"},
		{"truncated reply", map[string]any{"responseFormat": "json"}, port.LLMResponse{Content: `{"n":`, StopReason: port.StopMaxTokens}, nil, nil, "token limit"},
		{"trailing text", map[string]any{"responseFormat": "json"}, port.LLMResponse{Content: `{} extra`}, nil, nil, "not valid JSON"},
		{"missing schema", map[string]any{"responseFormat": "jsonSchema"}, port.LLMResponse{}, nil, nil, "schema"},
		{"bad schema", map[string]any{"responseFormat": "jsonSchema", "schema": "[1]"}, port.LLMResponse{}, nil, nil, "JSON object"},
		{"unknown format", map[string]any{"responseFormat": "xml"}, port.LLMResponse{}, nil, nil, "unknown response format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &fakeLLM{reply: func(int, port.LLMRequest) (port.LLMResponse, error) { return tt.reply, nil }}
			params := map[string]any{"message": "go"}
			for k, v := range tt.params {
				params[k] = v
			}
			n, _, in := setup(llm, params, map[string]any{})
			out, err := n.Execute(context.Background(), in)
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want %q", err, tt.errHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := json.Marshal(out.Ports[0][0].JSON["json"]); string(got) != mustJSON(tt.want) {
				t.Fatalf("json = %s, want %s", got, mustJSON(tt.want))
			}
			if tt.check != nil {
				tt.check(t, llm.reqs[0])
			}
		})
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestErrors(t *testing.T) {
	llmErr := &port.LLMError{Provider: "anthropic", Status: 429, Message: "slow down", Retryable: true}
	tests := []struct {
		name    string
		factory *fakeFactory
		params  map[string]any
		reply   func(int, port.LLMRequest) (port.LLMResponse, error)
		creds   bool
		check   func(t *testing.T, err error)
	}{
		{"provider error is wrapped and typed", nil, map[string]any{"message": "x"},
			func(int, port.LLMRequest) (port.LLMResponse, error) { return port.LLMResponse{}, llmErr }, true,
			func(t *testing.T, err error) {
				var le *port.LLMError
				var ne *nodesdk.NodeError
				if !errors.As(err, &le) || !le.Retryable || !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 0 || !strings.Contains(err.Error(), "calling anthropic") {
					t.Fatalf("err = %v", err)
				}
			}},
		{"empty message", nil, map[string]any{"message": "  "}, okReply("x"), true, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "message is required") {
				t.Fatal(err)
			}
		}},
		{"negative max tokens", nil, map[string]any{"message": "x", "maxTokens": float64(-1)}, okReply("x"), true, func(t *testing.T, err error) {
			if err == nil {
				t.Fatal("want error")
			}
		}},
		{"unknown provider", nil, map[string]any{"message": "x", "provider": "bard"}, okReply("x"), true, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "unknown provider") {
				t.Fatal(err)
			}
		}},
		{"no credential bound", nil, map[string]any{"message": "x"}, okReply("x"), false, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "loading credential") {
				t.Fatal(err)
			}
		}},
		{"factory failure", &fakeFactory{err: &port.LLMConfigError{Provider: "anthropic", Message: "API key is required"}}, map[string]any{"message": "x"}, okReply("x"), true, func(t *testing.T, err error) {
			var ce *port.LLMConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v", err)
			}
		}},
		{"no model for provider", nil, map[string]any{"message": "x", "provider": "azureopenai"}, okReply("x"), true, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "model is required") {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &fakeLLM{reply: tt.reply}
			n, ff, in := setup(llm, tt.params, map[string]any{})
			if tt.factory != nil {
				n = aichat.New(aichat.Config{Factory: tt.factory, DefaultModels: defaults})
			}
			_ = ff
			if !tt.creds {
				in.Credentials = nil
			}
			if tt.name == "no model for provider" {
				in.Credentials = map[string]nodesdk.Credential{"engi.azureOpenAI": {Data: map[string]any{}}}
			}
			_, err := n.Execute(context.Background(), in)
			tt.check(t, err)
		})
	}
}

func TestUnavailableWithoutFactory(t *testing.T) {
	n := aichat.New(aichat.Config{})
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"message": "x"})
	if _, err := n.Execute(context.Background(), in); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoItemsMakesNoCall(t *testing.T) {
	llm := &fakeLLM{reply: okReply("x")}
	n, _, in := setup(llm, map[string]any{"message": "x"})
	out := nodetest.Run(t, n, in)
	if len(out.Ports[0]) != 0 || len(llm.reqs) != 0 {
		t.Fatalf("out %v reqs %d", out.Ports, len(llm.reqs))
	}
}
