package aiembeddings_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aiembeddings"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(context.Context, []string) (port.EmbedResult, error) {
	return port.EmbedResult{}, nil
}

type fakeFactory struct {
	provider string
	set      port.EmbedderSettings
	cred     nodesdk.Credential
	err      error
}

func (f *fakeFactory) New(provider string, cred nodesdk.Credential, set port.EmbedderSettings, _ nodesdk.HTTPClient) (port.Embedder, error) {
	f.provider, f.set, f.cred = provider, set, cred
	return fakeEmbedder{}, f.err
}

var defaults = map[string]string{port.LLMProviderOpenAI: "text-embedding-3-small", port.LLMProviderOllama: "nomic-embed-text"}

func supply(ff *fakeFactory, params map[string]any) (any, error) {
	n := aiembeddings.New(aiembeddings.Config{Factory: ff, DefaultModels: defaults})
	in := nodetest.New(n.Describe(), nil, params)
	in.Credentials = map[string]nodesdk.Credential{
		"engi.openai": {Type: "engi.openai", Data: map[string]any{"apiKey": "k"}},
		"engi.ollama": {Type: "engi.ollama", Data: map[string]any{}},
	}
	return n.Supply(context.Background(), in)
}

func TestDescriptor(t *testing.T) {
	d := aiembeddings.New(aiembeddings.Config{}).Describe()
	if err := d.Validate(); err != nil || !d.IsSubNode() {
		t.Fatalf("err=%v subnode=%v", err, d.IsSubNode())
	}
	for _, o := range d.Properties[0].Options {
		if o.Value == port.LLMProviderAnthropic {
			t.Fatal("anthropic offered")
		}
	}
}

func TestSupply(t *testing.T) {
	ff := &fakeFactory{}
	got, err := supply(ff, map[string]any{"dimensions": float64(256)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(port.Embedder); !ok {
		t.Fatalf("value %T", got)
	}
	if ff.provider != "openai" || ff.set.Model != "text-embedding-3-small" || ff.set.Dimensions != 256 || ff.cred.Data["apiKey"] != "k" {
		t.Fatalf("factory saw %+v", ff)
	}
	if _, err := supply(ff, map[string]any{"provider": "ollama", "model": " bge-m3 "}); err != nil || ff.set.Model != "bge-m3" || ff.provider != "ollama" {
		t.Fatalf("err=%v %+v", err, ff)
	}
}

func TestSupplyErrors(t *testing.T) {
	for name, tt := range map[string]struct {
		params map[string]any
		ff     *fakeFactory
		want   string
	}{
		"anthropic":      {map[string]any{"provider": "anthropic"}, &fakeFactory{}, "unknown embeddings provider"},
		"azure no model": {map[string]any{"provider": "azureopenai"}, &fakeFactory{}, "model is required"},
		"neg dims":       {map[string]any{"dimensions": float64(-1)}, &fakeFactory{}, "dimensions"},
		"factory":        {nil, &fakeFactory{err: errors.New("boom")}, "boom"},
		"no credential":  {map[string]any{"provider": "azureopenai", "model": "d"}, &fakeFactory{}, "credential"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := supply(tt.ff, tt.params); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUnavailableWithoutFactory(t *testing.T) {
	n := aiembeddings.New(aiembeddings.Config{})
	if _, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
