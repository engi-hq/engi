package aitoolcode_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/aitoolcode"
	"github.com/engi-hq/engi/internal/node/code"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
)

func tool(t *testing.T, params map[string]any) port.Tool {
	t.Helper()
	n := aitoolcode.New(aitoolcode.Config{Limits: code.Limits{Timeout: 200 * time.Millisecond, MaxCallStackDepth: 64, MaxOutputBytes: 1 << 10}})
	v, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, params))
	if err != nil {
		t.Fatalf("Supply: %v", err)
	}
	tools, err := v.(port.ToolProvider).Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("Tools = %v, %v", tools, err)
	}
	return tools[0]
}

func TestDescriptor(t *testing.T) {
	d := aitoolcode.New(aitoolcode.Config{}).Describe()
	if err := d.Validate(); err != nil || !d.IsSubNode() {
		t.Fatalf("invalid: %v", err)
	}
}

func TestCallReturnsJSONAndStrings(t *testing.T) {
	tl := tool(t, map[string]any{"name": "add", "description": "adds",
		"parameters": `{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`,
		"jsCode":     `return {sum: args.a + args.b};`})
	if tl.Definition().Name != "add" || !strings.Contains(string(tl.Definition().Parameters), `"a"`) {
		t.Fatalf("definition = %+v", tl.Definition())
	}
	res, err := tl.Call(context.Background(), json.RawMessage(`{"a":1,"b":2}`))
	if err != nil || res.IsError || res.Content != `{"sum":3}` {
		t.Fatalf("result = %+v, %v", res, err)
	}
	str := tool(t, map[string]any{"jsCode": `return "hi " + args.input;`})
	res, _ = str.Call(context.Background(), json.RawMessage(`{"input":"you"}`))
	if res.Content != "hi you" {
		t.Fatalf("string result = %+v", res)
	}
}

func TestToolErrorsAreResults(t *testing.T) {
	tl := tool(t, map[string]any{"jsCode": `throw new Error("nope")`})
	res, err := tl.Call(context.Background(), json.RawMessage(`{"input":"x"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "nope") {
		t.Fatalf("result = %+v, %v", res, err)
	}
	res, err = tl.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "missing required argument") {
		t.Fatalf("missing arg = %+v, %v", res, err)
	}
	res, _ = tl.Call(context.Background(), json.RawMessage(`[1]`))
	if !res.IsError {
		t.Fatal("non-object arguments must be an error result")
	}
}

func TestSupplyValidation(t *testing.T) {
	n := aitoolcode.New(aitoolcode.Config{})
	for name, params := range map[string]map[string]any{
		"bad name":     {"name": "has space"},
		"bad schema":   {"parameters": `{"type":"array"}`},
		"bad required": {"parameters": `{"type":"object","required":["x"]}`},
		"syntax":       {"jsCode": `return (;`},
	} {
		if _, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, params)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
