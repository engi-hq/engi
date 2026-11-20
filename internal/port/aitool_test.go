package port_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/engi-hq/engi/internal/port"
)

type fakeTool struct{ name string }

func (f fakeTool) Definition() port.ToolDefinition { return port.ToolDefinition{Name: f.name} }
func (f fakeTool) Call(_ context.Context, args json.RawMessage) (port.ToolResult, error) {
	return port.ToolResult{Content: f.name + string(args)}, nil
}

func TestToolset(t *testing.T) {
	ts, err := port.NewToolset(fakeTool{"b"}, fakeTool{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ts.Names(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("names = %v", got)
	}
	if defs := ts.Definitions(); len(defs) != 2 || defs[0].Name != "b" {
		t.Fatalf("definitions must keep insertion order: %v", defs)
	}
	res, err := ts.Call(context.Background(), "a", json.RawMessage(`{}`))
	if err != nil || res.Content != "a{}" {
		t.Fatalf("call = %+v %v", res, err)
	}
	if _, err := ts.Call(context.Background(), "zzz", nil); !errors.Is(err, port.ErrUnknownTool) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := ts.Lookup("b"); !ok {
		t.Fatal("lookup b")
	}
}

func TestToolsetRejects(t *testing.T) {
	if _, err := port.NewToolset(fakeTool{"a"}, fakeTool{"a"}); !errors.Is(err, port.ErrDuplicateTool) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := port.NewToolset(fakeTool{""}); err == nil {
		t.Fatal("empty name must fail")
	}
}

func TestNilToolsetIsEmpty(t *testing.T) {
	var ts *port.Toolset
	if _, ok := ts.Lookup("x"); ok || ts.Definitions() != nil || ts.Names() != nil {
		t.Fatal("nil toolset must be empty")
	}
}

func TestLLMErrorText(t *testing.T) {
	cause := errors.New("dial refused")
	e := &port.LLMError{Provider: "ollama", Status: 503, Message: "busy", Err: cause}
	if got := e.Error(); got != "ollama HTTP 503: busy: dial refused" {
		t.Fatalf("got %q", got)
	}
	if !errors.Is(e, cause) || port.IsRetryableLLMError(e) {
		t.Fatal("unwrap or retryable wrong")
	}
}
