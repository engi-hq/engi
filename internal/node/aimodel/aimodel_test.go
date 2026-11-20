package aimodel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/engi-hq/engi/internal/node/aimodel"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeFactory struct {
	provider string
	cred     nodesdk.Credential
	err      error
}

func (f *fakeFactory) New(provider string, cred nodesdk.Credential, _ nodesdk.HTTPClient) (port.LLM, error) {
	f.provider, f.cred = provider, cred
	return nil, f.err
}

var defaults = map[string]string{port.LLMProviderAnthropic: "claude-sonnet-5-5"}

func supply(t *testing.T, ff *fakeFactory, params map[string]any) (any, error) {
	t.Helper()
	n := aimodel.New(aimodel.Config{Factory: ff, DefaultModels: defaults})
	in := nodetest.New(n.Describe(), nil, params)
	in.Credentials = map[string]nodesdk.Credential{"engi.anthropic": {Type: "engi.anthropic", Data: map[string]any{"apiKey": "k"}}}
	return n.Supply(context.Background(), in)
}

func TestDescriptorIsSubNode(t *testing.T) {
	d := aimodel.New(aimodel.Config{}).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.IsSubNode() || d.Outputs[0].Kind != nodesdk.PortAIModel {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestSupplyBuildsTemplate(t *testing.T) {
	ff := &fakeFactory{}
	v, err := supply(t, ff, map[string]any{"temperature": 0.2, "maxTokens": float64(50), "options": map[string]any{"topP": 0.9, "stopSequences": "A, B"}})
	if err != nil {
		t.Fatal(err)
	}
	_, req, err := v.(port.ModelProvider).Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ff.provider != "anthropic" || ff.cred.Data["apiKey"] != "k" {
		t.Fatalf("factory got %q %v", ff.provider, ff.cred)
	}
	if req.Model != "claude-sonnet-5-5" || *req.Temperature != 0.2 || req.MaxTokens != 50 || *req.TopP != 0.9 || len(req.Stop) != 2 {
		t.Fatalf("req = %+v", req)
	}
}

func TestSupplyErrors(t *testing.T) {
	if _, err := supply(t, &fakeFactory{}, map[string]any{"provider": "nope"}); err == nil {
		t.Fatal("unknown provider must fail")
	}
	if _, err := supply(t, &fakeFactory{}, map[string]any{"maxTokens": float64(-1)}); err == nil {
		t.Fatal("negative maxTokens must fail")
	}
	if _, err := supply(t, &fakeFactory{err: errors.New("boom")}, nil); err == nil {
		t.Fatal("factory error must fail")
	}
	n := aimodel.New(aimodel.Config{})
	if _, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("Execute err = %v", err)
	}
}
