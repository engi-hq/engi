package expression_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/expression"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var testNow = time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)

type fakeNodes struct {
	paired map[string]domain.Item
	output map[string][]domain.Item
}

func (f fakeNodes) PairedItem(node string) (domain.Item, error) {
	if it, ok := f.paired[node]; ok {
		return it, nil
	}
	return domain.Item{}, fmt.Errorf("node %q has no paired item: %w", node, domain.ErrNotFound)
}

func (f fakeNodes) Output(node string) ([]domain.Item, error) {
	if it, ok := f.output[node]; ok {
		return it, nil
	}
	return nil, fmt.Errorf("node %q has not run: %w", node, domain.ErrNotFound)
}

func newEvaluator(t *testing.T, mutate func(*config.ExpressionConfig)) *expression.Evaluator {
	t.Helper()
	cfg := config.Default().Expression
	cfg.EnvAllowList = []string{"ALLOWED"}
	if mutate != nil {
		mutate(&cfg)
	}
	env := map[string]string{"ALLOWED": "yes", "SECRET": "nope"}
	e, err := expression.New(cfg, fixedClock{testNow}, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func scope() port.ExpressionScope {
	cur := domain.Item{JSON: map[string]any{
		"name": "Ada", "n": float64(3), "items": []any{"a", "b", "c"},
		"nested": map[string]any{"k": "v"},
	}, Binary: map[string]domain.BinaryRef{"file": {Ref: "blob://1", MIME: "text/plain", Name: "f.txt", Size: 12}}}
	other := domain.Item{JSON: map[string]any{"id": float64(7)}}
	return port.ExpressionScope{
		Item:      cur,
		Input:     []domain.Item{{JSON: map[string]any{"x": float64(1)}}, cur, {JSON: map[string]any{"x": float64(3)}}},
		ItemIndex: 1,
		RunIndex:  2,
		Nodes: fakeNodes{
			paired: map[string]domain.Item{"Other": other},
			output: map[string][]domain.Item{"Other": {other, {JSON: map[string]any{"id": float64(8)}}}},
		},
		Vars:      map[string]any{"apiBase": "https://example.test"},
		Execution: port.ExecutionMeta{ID: "ex_1", Mode: domain.ModeManual},
		Workflow:  port.WorkflowMeta{ID: "wf_1", Name: "Order sync"},
	}
}

func TestExecutionResumeURL(t *testing.T) {
	e := newEvaluator(t, nil)
	tests := []struct {
		name      string
		resumeURL string
		tpl       string
		want      any
	}{
		{"absolute resume url from the scope", "https://engi.example.com/hook-wait/ex_1.secret", "={{ $execution.resumeUrl }}", "https://engi.example.com/hook-wait/ex_1.secret"},
		{"url embedded in text", "https://engi.example.com/hook-wait/ex_1.secret", "=Resume: {{ $execution.resumeUrl }}", "Resume: https://engi.example.com/hook-wait/ex_1.secret"},
		{"empty when no link is known", "", "={{ $execution.resumeUrl }}", ""},
		{"id and mode still present", "https://engi.example.com/hook-wait/ex_1.secret", "={{ $execution.id }}/{{ $execution.mode }}", "ex_1/manual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := scope()
			sc.Execution.ResumeURL = tt.resumeURL
			got, err := e.Resolve(context.Background(), tt.tpl, sc)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	e := newEvaluator(t, nil)
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"plain string untouched", "hello {{ not evaluated }}", "hello {{ not evaluated }}"},
		{"non-string untouched", 42, 42},
		{"nil untouched", nil, nil},
		{"literal template", "=just text", "just text"},
		{"interpolation", "=Hello {{ $json.name }}!", "Hello Ada!"},
		{"multiple blocks", "={{ $json.name }}-{{ $json.n }}", "Ada-3"},
		{"single block keeps number", "={{ $json.n * 2 }}", float64(6)},
		{"single block keeps boolean", "={{ $json.items.length > 0 }}", true},
		{"single block keeps array", "={{ $json.items }}", []any{"a", "b", "c"}},
		{"single block keeps object", "={{ $json.nested }}", map[string]any{"k": "v"}},
		{"isExecuted true", `={{ $("Other").isExecuted }}`, true},
		{"isExecuted false", `={{ $("Nope").isExecuted }}`, false},
		{"integer result stays a number", "={{ 6 / 2 }}", float64(3)},
		{"fractional result", "={{ 10 / 4 }}", 2.5},
		{"single block null", "={{ null }}", nil},
		{"single block undefined", "={{ $json.missing }}", nil},
		{"undefined interpolates empty", "=[{{ $json.missing }}]", "[]"},
		{"object interpolates as JSON", "=v={{ $json.nested }}", `v={"k":"v"}`},
		{"object literal inside block", "={{ {a: {b: 1}}.a.b }}", float64(1)},
		{"close braces in string", `={{ "}}" + "x" }}`, "}}x"},
		{"template literal", "={{ `${$json.name}!` }}", "Ada!"},
		{"line comment in block", "={{ $json.n // comment\n }}", float64(3)},
		{"nested map resolved", map[string]any{"a": "={{ $json.n }}", "b": []any{"={{ $json.name }}", "plain"}},
			map[string]any{"a": float64(3), "b": []any{"Ada", "plain"}}},
		{"$input.item", "={{ $input.item.json.name }}", "Ada"},
		{"$input.all", "={{ $input.all().length }}", float64(3)},
		{"$input.first", "={{ $input.first().json.x }}", float64(1)},
		{"$input.last", "={{ $input.last().json.x }}", float64(3)},
		{"$binary", "={{ $binary.file.mime }}", "text/plain"},
		{"$node shorthand", `={{ $node["Other"].json.id }}`, float64(7)},
		{"$() item", `={{ $("Other").item.json.id }}`, float64(7)},
		{"$() all", `={{ $("Other").all().length }}`, float64(2)},
		{"$() first", `={{ $("Other").first().json.id }}`, float64(7)},
		{"$() last", `={{ $("Other").last().json.id }}`, float64(8)},
		{"$vars", "={{ $vars.apiBase }}/orders", "https://example.test/orders"},
		{"$env allowed", "={{ $env.ALLOWED }}", "yes"},
		{"$env denied", "={{ $env.SECRET }}", nil},
		{"$env keys", "={{ Object.keys($env).join() }}", "ALLOWED"},
		{"$execution", "={{ $execution.id }}:{{ $execution.mode }}", "ex_1:manual"},
		{"$execution.resumeUrl", "={{ $execution.resumeUrl }}", ""},
		{"$workflow", "={{ $workflow.name }}", "Order sync"},
		{"$itemIndex $runIndex", "={{ $itemIndex }}/{{ $runIndex }}", "1/2"},
		{"$now from clock", "={{ $now.toISOString() }}", "2026-03-14T15:09:26.000Z"},
		{"$today midnight", "={{ $today.toISOString() }}", "2026-03-14T00:00:00.000Z"},
		{"$fn.sha256", `={{ $fn.sha256("abc") }}`, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"$fn.base64 roundtrip", `={{ $fn.base64Decode($fn.base64Encode("héllo")) }}`, "héllo"},
		{"$fn.formatDate", `={{ $fn.formatDate($now, "YYYY-MM-DD HH:mm:ss") }}`, "2026-03-14 15:09:26"},
		{"array methods on input data", "={{ $json.items.map(function (s) { return s.toUpperCase(); }).join('') }}", "ABC"},
		{"arrow functions", "={{ $json.items.filter(s => s !== 'b').length }}", float64(2)},
		{"JSON in scope", "={{ JSON.stringify($json.nested) }}", `{"k":"v"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Resolve(context.Background(), tt.in, scope())
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	e := newEvaluator(t, func(c *config.ExpressionConfig) {
		c.MaxTemplateLength = 64
		c.MaxOutputBytes = 32
		c.EvalTimeout = 100 * time.Millisecond
	})
	tests := []struct {
		name    string
		in      string
		wantIs  error
		wantMsg string
	}{
		{"syntax error", "={{ 1 + }}", nil, "syntax"},
		{"unterminated block", "=hello {{ $json.name", nil, "unterminated"},
		{"runtime error", "={{ $json.nope.deeper }}", nil, "TypeError"},
		{"unknown node", `={{ $("Missing").first() }}`, domain.ErrNotFound, "has not run"},
		{"no paired item", `={{ $node["Missing"].json }}`, domain.ErrNotFound, ""},
		{"template too long", "=" + strings.Repeat("a", 65), expression.ErrTemplateTooLong, ""},
		{"output too large string", `={{ "x".repeat(100) }}`, expression.ErrOutputTooLarge, ""},
		{"output too large interpolation", `=a{{ "x".repeat(20) }}{{ "y".repeat(20) }}`, expression.ErrOutputTooLarge, ""},
		{"output too large object", `={{ ({a: "x".repeat(100)}) }}`, expression.ErrOutputTooLarge, ""},
		{"NaN not serializable", "={{ 0/0 }}", expression.ErrNotSerializable, ""},
		{"function not serializable", "={{ function () {} }}", expression.ErrNotSerializable, ""},
		{"infinite loop times out", "={{ (function () { while (true) {} })() }}", expression.ErrTimeout, ""},
		{"deep recursion fails", "={{ (function f() { return f(); })() }}", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.Resolve(context.Background(), tt.in, scope())
			var xe *domain.ExpressionError
			if !errors.As(err, &xe) {
				t.Fatalf("want *domain.ExpressionError, got %T: %v", err, err)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error %v does not wrap %v", err, tt.wantIs)
			}
			if tt.wantMsg != "" && !strings.Contains(strings.ToLower(xe.Message), strings.ToLower(tt.wantMsg)) {
				t.Errorf("message %q does not contain %q", xe.Message, tt.wantMsg)
			}
			if xe.Expression == "" {
				t.Error("error must carry the expression")
			}
		})
	}
}

func TestRuntimeErrorCarriesStack(t *testing.T) {
	e := newEvaluator(t, nil)
	_, err := e.Resolve(context.Background(), "={{ $json.nope.deeper }}", scope())
	var xe *domain.ExpressionError
	if !errors.As(err, &xe) || xe.Stack == "" {
		t.Fatalf("expected a stack excerpt, got %+v", xe)
	}
}

func TestSandboxHasNoHostAccess(t *testing.T) {
	e := newEvaluator(t, nil)
	for _, name := range []string{"require", "process", "setTimeout", "setInterval", "fetch", "XMLHttpRequest", "module", "console", "Buffer"} {
		got, err := e.Resolve(context.Background(), "={{ typeof "+name+" }}", scope())
		if err != nil {
			t.Fatalf("typeof %s: %v", name, err)
		}
		if got != "undefined" {
			t.Errorf("typeof %s = %v, want undefined", name, got)
		}
	}
}

func TestEvaluationsDoNotLeakState(t *testing.T) {
	// One cached VM is reused; make sure a previous expression cannot poison it.
	e := newEvaluator(t, func(c *config.ExpressionConfig) { c.ProgramCacheSize = 1 })
	attacks := []string{
		"={{ (function () { Object.prototype.polluted = 1; return 1; })() }}",
		"={{ (function () { Array.prototype.polluted = 1; return 1; })() }}",
		"={{ (function () { globalThis.leak = 1; return 1; })() }}",
		"={{ (function () { (0, eval)('var leak2 = 1'); return 1; })() }}",
	}
	for _, a := range attacks {
		_, _ = e.Resolve(context.Background(), a, scope()) // may fail; must not pollute
	}
	for _, probe := range []string{
		"={{ typeof ({}).polluted }}", "={{ typeof [].polluted }}", "={{ typeof globalThis.leak }}", "={{ typeof leak2 }}",
	} {
		got, err := e.Resolve(context.Background(), probe, scope())
		if err != nil || got != "undefined" {
			t.Errorf("%s = %v (%v), want undefined", probe, got, err)
		}
	}
}

func TestExpressionCannotMutateEngineData(t *testing.T) {
	e := newEvaluator(t, nil)
	sc := scope()
	_, err := e.Resolve(context.Background(), "={{ ($json.name = 'Evil', $json.items.push('z'), $input.all()[0].json.x = 99, 1) }}", sc)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Item.JSON["name"] != "Ada" || len(sc.Item.JSON["items"].([]any)) != 3 || sc.Input[0].JSON["x"] != float64(1) {
		t.Fatalf("engine data was mutated: %+v", sc.Item.JSON)
	}
}

func TestContextCancellation(t *testing.T) {
	e := newEvaluator(t, func(c *config.ExpressionConfig) { c.EvalTimeout = 10 * time.Second })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := e.Resolve(ctx, "={{ (function () { while (true) {} })() }}", scope())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// Already-cancelled context fails before running anything.
	if _, err := e.Resolve(ctx, "={{ 1 }}", scope()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The pooled VM remains usable after an interrupt.
	if got, err := e.Resolve(context.Background(), "={{ 1 + 1 }}", scope()); err != nil || got != float64(2) {
		t.Fatalf("after interrupt: %v %v", got, err)
	}
}

func TestVMReuseAfterTimeout(t *testing.T) {
	e := newEvaluator(t, func(c *config.ExpressionConfig) { c.EvalTimeout = 50 * time.Millisecond })
	if _, err := e.Resolve(context.Background(), "={{ (function () { while (true) {} })() }}", scope()); !errors.Is(err, expression.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if got, err := e.Resolve(context.Background(), "={{ 2 * 3 }}", scope()); err != nil || got != float64(6) {
		t.Fatalf("after timeout: %v %v", got, err)
	}
}

func TestConcurrentEvaluation(t *testing.T) {
	e := newEvaluator(t, func(c *config.ExpressionConfig) { c.ProgramCacheSize = 2 })
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				tpl := fmt.Sprintf("={{ $json.n + %d }}", (i+j)%5) // more templates than cache slots
				got, err := e.Resolve(context.Background(), tpl, scope())
				want := float64(3 + (i+j)%5)
				if err != nil || got != want {
					t.Errorf("%s = %v (%v), want %v", tpl, got, err, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNewValidatesInputs(t *testing.T) {
	bad := config.Default().Expression
	bad.EvalTimeout = 0
	if _, err := expression.New(bad, fixedClock{}, func(string) (string, bool) { return "", false }); err == nil {
		t.Error("invalid config must fail")
	}
	if _, err := expression.New(config.Default().Expression, nil, func(string) (string, bool) { return "", false }); err == nil {
		t.Error("nil clock must fail")
	}
}

func TestNodesNilGivesError(t *testing.T) {
	e := newEvaluator(t, nil)
	sc := scope()
	sc.Nodes = nil
	_, err := e.Resolve(context.Background(), `={{ $("A").first() }}`, sc)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
