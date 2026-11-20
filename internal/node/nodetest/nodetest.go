// Package nodetest provides a fake nodesdk.Input so node packages can be
// tested without the engine.
package nodetest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Input is an in-memory nodesdk.Input. Parameters come from Params, falling
// back to the descriptor default; ParamFunc, when set, overrides both and
// stands in for expression evaluation.
type Input struct {
	Desc      nodesdk.Descriptor
	Ports     [][]nodesdk.Item
	Params    map[string]any
	ParamFunc func(name string, itemIndex int) (any, bool)
	// EvalFunc stands in for EvalExpression.
	EvalFunc func(expression string, itemIndex int) (any, error)
	Client   nodesdk.HTTPClient
	Info     nodesdk.ExecutionInfo
	// Credentials are returned by Credential, keyed by binding name.
	Credentials map[string]nodesdk.Credential
	// Subs are the values SubPort returns per sub-port kind, standing in for
	// the engine pulling connected sub-nodes. Use SupplyFrom to fill it from
	// real sub-node implementations.
	Subs map[nodesdk.PortKind][]nodesdk.Supplied
	// Blobs backs Blob; nil makes Blob fail with ErrUnavailable. Use NewMemoryBlobs.
	Blobs nodesdk.BlobAccess
}

var _ nodesdk.Input = (*Input)(nil)

// New builds an Input with items on port 0.
func New(desc nodesdk.Descriptor, items []nodesdk.Item, params map[string]any) *Input {
	return &Input{Desc: desc, Ports: [][]nodesdk.Item{items}, Params: params}
}

// Items implements nodesdk.Input.
func (i *Input) Items(port int) []nodesdk.Item {
	if port < 0 || port >= len(i.Ports) {
		return nil
	}
	return i.Ports[port]
}

// Param implements nodesdk.Input.
func (i *Input) Param(_ context.Context, name string, itemIndex int) (nodesdk.Value, error) {
	if i.ParamFunc != nil {
		if v, ok := i.ParamFunc(name, itemIndex); ok {
			return v, nil
		}
	}
	if v, ok := i.Params[name]; ok {
		return v, nil
	}
	for _, p := range i.Desc.Properties {
		if p.Name != name {
			continue
		}
		if p.Default != nil {
			return p.Default, nil
		}
		if p.Required {
			return nil, fmt.Errorf("parameter %q: %w", name, nodesdk.ErrParamMissing)
		}
	}
	return nil, nil
}

// EvalExpression implements nodesdk.Input. It calls EvalFunc and fails with
// ErrUnavailable when none is set.
func (i *Input) EvalExpression(_ context.Context, expression string, itemIndex int) (nodesdk.Value, error) {
	if i.EvalFunc == nil {
		return nil, nodesdk.ErrUnavailable
	}
	return i.EvalFunc(expression, itemIndex)
}

// SubPort implements nodesdk.Input. It returns Subs[kind] and fails when the
// kind is not a sub-port input of Desc, like the engine does.
func (i *Input) SubPort(_ context.Context, kind nodesdk.PortKind, _ int) ([]nodesdk.Supplied, error) {
	spec, ok := i.Desc.PortOfKind(true, kind, 0)
	if !ok || !kind.IsSub() {
		return nil, fmt.Errorf("node has no %q sub-port input", kind)
	}
	got := i.Subs[kind]
	if spec.Required && len(got) == 0 {
		return nil, fmt.Errorf("sub-port %q is required but no sub-node is connected", kind)
	}
	return got, nil
}

// SupplyFrom runs a sub-node's Supply against sub (its own fake Input) and
// returns the result in the shape of Input.Subs, so a parent can be tested
// with the real sub-node.
func SupplyFrom(ctx context.Context, name string, s nodesdk.Supplier, sub nodesdk.Input) ([]nodesdk.Supplied, error) {
	v, err := s.Supply(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("sub-node %q: %w", name, err)
	}
	return []nodesdk.Supplied{{Node: name, Value: v}}, nil
}

// Credential implements nodesdk.Input.
func (i *Input) Credential(_ context.Context, name string) (nodesdk.Credential, error) {
	if c, ok := i.Credentials[name]; ok {
		return c, nil
	}
	return nodesdk.Credential{}, nodesdk.ErrUnavailable
}

// Blob implements nodesdk.Input.
func (i *Input) Blob() nodesdk.BlobAccess {
	if i.Blobs != nil {
		return i.Blobs
	}
	return nodesdk.UnavailableBlob{}
}

// HTTP implements nodesdk.Input.
func (i *Input) HTTP() nodesdk.HTTPClient {
	if i.Client != nil {
		return i.Client
	}
	return nodesdk.UnavailableHTTP{}
}

// Logger implements nodesdk.Input.
func (i *Input) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ExecutionInfo implements nodesdk.Input.
func (i *Input) ExecutionInfo() nodesdk.ExecutionInfo { return i.Info }

// Items builds items from JSON objects.
func Items(objs ...map[string]any) []nodesdk.Item {
	out := make([]nodesdk.Item, len(objs))
	for n, o := range objs {
		out[n] = nodesdk.Item{JSON: o}
	}
	return out
}

// JSONs extracts the JSON of each item of a port.
func JSONs(items []nodesdk.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for n, it := range items {
		out[n] = it.JSON
	}
	return out
}

// RoundTripFunc adapts a function to nodesdk.HTTPClient.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// Do implements nodesdk.HTTPClient.
func (f RoundTripFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Run executes a node and fails the test on error.
func Run(t *testing.T, n nodesdk.Node, in nodesdk.Input) nodesdk.Output {
	t.Helper()
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out
}
