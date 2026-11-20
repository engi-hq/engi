package nodesdk_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

type paramInput struct{ values map[string]any }

func (p paramInput) Items(int) []nodesdk.Item { return nil }
func (p paramInput) Param(_ context.Context, name string, _ int) (nodesdk.Value, error) {
	return p.values[name], nil
}
func (paramInput) EvalExpression(context.Context, string, int) (nodesdk.Value, error) {
	return nil, nodesdk.ErrUnavailable
}
func (paramInput) SubPort(context.Context, nodesdk.PortKind, int) ([]nodesdk.Supplied, error) {
	return nil, nil
}
func (paramInput) Credential(context.Context, string) (nodesdk.Credential, error) {
	return nodesdk.Credential{}, nil
}
func (paramInput) Blob() nodesdk.BlobAccess             { return nil }
func (paramInput) HTTP() nodesdk.HTTPClient             { return nil }
func (paramInput) Logger() *slog.Logger                 { return nil }
func (paramInput) ExecutionInfo() nodesdk.ExecutionInfo { return nodesdk.ExecutionInfo{} }

func TestTypedParams(t *testing.T) {
	in := paramInput{map[string]any{"s": "x", "b": true, "f": float64(3), "i64": int64(4), "frac": 1.5, "nil": nil}}
	ctx := context.Background()
	if s, err := nodesdk.ParamString(ctx, in, "s", 0); err != nil || s != "x" {
		t.Errorf("string: %q %v", s, err)
	}
	if s, err := nodesdk.ParamString(ctx, in, "nil", 0); err != nil || s != "" {
		t.Errorf("nil string: %q %v", s, err)
	}
	if _, err := nodesdk.ParamString(ctx, in, "b", 0); err == nil {
		t.Error("bool as string must fail")
	}
	if b, err := nodesdk.ParamBool(ctx, in, "b", 0); err != nil || !b {
		t.Errorf("bool: %v %v", b, err)
	}
	if _, err := nodesdk.ParamBool(ctx, in, "s", 0); err == nil {
		t.Error("string as bool must fail")
	}
	for name, want := range map[string]int{"f": 3, "i64": 4, "nil": 0} {
		if n, err := nodesdk.ParamInt(ctx, in, name, 0); err != nil || n != want {
			t.Errorf("int %s: %d %v", name, n, err)
		}
	}
	if _, err := nodesdk.ParamInt(ctx, in, "frac", 0); err == nil {
		t.Error("fraction as int must fail")
	}
	if _, err := nodesdk.ParamInt(ctx, in, "s", 0); err == nil {
		t.Error("string as int must fail")
	}
}
