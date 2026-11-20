package code

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 3, 1, 15, 4, 5, 0, time.UTC) }

func limits() Limits {
	return Limits{Timeout: 2 * time.Second, MaxCallStackDepth: 128, MaxOutputBytes: 1 << 20}
}

func newNode() nodesdk.Node { return New(limits(), fixedClock{}) }

func exec(t *testing.T, items []nodesdk.Item, params map[string]any) ([]nodesdk.Item, error) {
	t.Helper()
	n := newNode()
	in := nodetest.New(n.Describe(), items, params)
	in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", WorkflowID: "wf_1", WorkflowName: "demo", Mode: "manual", RunIndex: 2}
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		return nil, err
	}
	return out.Ports[0], nil
}

func TestRunOnceForAllItems(t *testing.T) {
	items := nodetest.Items(map[string]any{"n": 1.0}, map[string]any{"n": 2.0}, map[string]any{"n": 3.0})
	tests := []struct {
		name string
		code string
		want []map[string]any
	}{
		{"default passes items through", "", []map[string]any{{"n": 1.0}, {"n": 2.0}, {"n": 3.0}}},
		{"map items", `return $input.all().map(i => ({ json: { n2: i.json.n * 2 } }));`,
			[]map[string]any{{"n2": 2.0}, {"n2": 4.0}, {"n2": 6.0}}},
		{"plain objects are items", `return [{ a: 1 }, { a: 2 }];`, []map[string]any{{"a": 1.0}, {"a": 2.0}}},
		{"single object", `return { total: $input.all().length };`, []map[string]any{{"total": 3.0}}},
		{"first and last", `return [{ f: $input.first().json.n, l: $input.last().json.n }];`, []map[string]any{{"f": 1.0, "l": 3.0}}},
		{"return nothing", `return;`, nil},
		{"return null", `return null;`, nil},
		{"empty array", `return [];`, nil},
		{"reduce to one item", `return [{ json: { sum: $input.all().reduce((s, i) => s + i.json.n, 0) } }];`, []map[string]any{{"sum": 6.0}}},
		{"scope vars", `return [{ e: $execution.id, m: $execution.mode, w: $workflow.name, r: $runIndex, i: $itemIndex }];`,
			[]map[string]any{{"e": "ex_1", "m": "manual", "w": "demo", "r": 2.0, "i": 0.0}}},
		{"now and today", `return [{ now: $now.toISOString(), today: $today.toISOString() }];`,
			[]map[string]any{{"now": "2026-03-01T15:04:05.000Z", "today": "2026-03-01T00:00:00.000Z"}}},
		{"dates are normalised", `return [{ d: new Date(0) }];`, []map[string]any{{"d": "1970-01-01T00:00:00Z"}}},
		{"nested data and arrays", `return [{ a: [1, { b: [2, 3] }], c: { d: null } }];`,
			[]map[string]any{{"a": []any{1.0, map[string]any{"b": []any{2.0, 3.0}}}, "c": map[string]any{"d": nil}}}},
		{"console is available", `console.log("hi", 1); return [{ ok: true }];`, []map[string]any{{"ok": true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{}
			if tt.code != "" {
				params["jsCode"] = tt.code
			}
			got, err := exec(t, items, params)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(nodetest.JSONs(got), tt.want) {
				t.Fatalf("got %v\nwant %v", nodetest.JSONs(got), tt.want)
			}
		})
	}
	t.Run("empty input", func(t *testing.T) {
		got, err := exec(t, nil, map[string]any{"jsCode": `return [{ n: $input.all().length }];`})
		if err != nil || !reflect.DeepEqual(nodetest.JSONs(got), []map[string]any{{"n": 0.0}}) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("first of empty input is undefined", func(t *testing.T) {
		got, err := exec(t, nil, map[string]any{"jsCode": `return [{ f: $input.first() === undefined }];`})
		if err != nil || got[0].JSON["f"] != true {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

func TestRunOnceForEachItem(t *testing.T) {
	items := nodetest.Items(map[string]any{"n": 1.0}, map[string]any{"n": 2.0}, map[string]any{"n": 3.0})
	tests := []struct {
		name string
		code string
		want []map[string]any
	}{
		{"transform each", `return { ...$json, sq: $json.n * $json.n, idx: $itemIndex };`,
			[]map[string]any{{"n": 1.0, "sq": 1.0, "idx": 0.0}, {"n": 2.0, "sq": 4.0, "idx": 1.0}, {"n": 3.0, "sq": 9.0, "idx": 2.0}}},
		{"null drops the item", `return $json.n === 2 ? null : $json;`, []map[string]any{{"n": 1.0}, {"n": 3.0}}},
		{"array fans out", `return [{ a: $json.n }, { a: -$json.n }];`,
			[]map[string]any{{"a": 1.0}, {"a": -1.0}, {"a": 2.0}, {"a": -2.0}, {"a": 3.0}, {"a": -3.0}}},
		{"current item through $input", `return { same: $input.item.json.n === $json.n, count: $input.all().length };`,
			[]map[string]any{{"same": true, "count": 3.0}, {"same": true, "count": 3.0}, {"same": true, "count": 3.0}}},
		{"undefined drops", `return undefined;`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exec(t, items, map[string]any{"mode": "runOnceForEachItem", "jsCode": tt.code})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(nodetest.JSONs(got), tt.want) {
				t.Fatalf("got %v\nwant %v", nodetest.JSONs(got), tt.want)
			}
		})
	}
	t.Run("pairing", func(t *testing.T) {
		got, _ := exec(t, items, map[string]any{"mode": "runOnceForEachItem", "jsCode": `return [{a:1},{a:2}];`})
		if len(got) != 6 || got[5].PairedItem[0].Item != 2 {
			t.Fatalf("pairing = %+v", got)
		}
	})
	t.Run("empty input runs nothing", func(t *testing.T) {
		got, err := exec(t, nil, map[string]any{"mode": "runOnceForEachItem", "jsCode": `throw new Error("never");`})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("error carries the item index", func(t *testing.T) {
		_, err := exec(t, items, map[string]any{"mode": "runOnceForEachItem", "jsCode": `if ($json.n === 2) { throw new Error("bad two"); } return $json;`})
		var ne *nodesdk.NodeError
		if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 1 || !strings.Contains(err.Error(), "bad two") {
			t.Fatalf("err = %#v", err)
		}
	})
}

func TestBinaryAndItemShape(t *testing.T) {
	items := []nodesdk.Item{{JSON: map[string]any{"a": 1.0}, Binary: map[string]nodesdk.BinaryRef{"f": {Ref: "blob1", MIME: "text/plain", Name: "x.txt", Size: 5}}}}
	got, err := exec(t, items, map[string]any{"jsCode": `
		const it = $input.first();
		return [{ json: { mime: it.binary.f.mime }, binary: it.binary }];`})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].JSON["mime"] != "text/plain" || got[0].Binary["f"] != (nodesdk.BinaryRef{Ref: "blob1", MIME: "text/plain", Name: "x.txt", Size: 5}) {
		t.Fatalf("got %+v", got[0])
	}
}

// In all-items mode $json and $binary are the first item's data;
// with no input they are empty objects. $input.item stays per-item only.
func TestJSONAndBinaryInAllItemsModeUseFirstItem(t *testing.T) {
	items := []nodesdk.Item{
		{JSON: map[string]any{"n": 1.0}, Binary: map[string]nodesdk.BinaryRef{"f": {Ref: "blob1", MIME: "text/plain"}}},
		{JSON: map[string]any{"n": 2.0}},
	}
	tests := []struct {
		name  string
		items []nodesdk.Item
		code  string
		want  []map[string]any
	}{
		{"json is the first item", items, `return [{ n: $json.n, count: $input.all().length }];`,
			[]map[string]any{{"n": 1.0, "count": 2.0}}},
		{"binary is the first item's", items, `return [{ mime: $binary.f.mime }];`, []map[string]any{{"mime": "text/plain"}}},
		{"json is empty without input", nil, `return [{ keys: Object.keys($json).length, bins: Object.keys($binary).length }];`,
			[]map[string]any{{"keys": 0.0, "bins": 0.0}}},
		{"item is not set in all-items mode", items, `return [{ hasItem: $input.item === undefined }];`,
			[]map[string]any{{"hasItem": true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exec(t, tt.items, map[string]any{"jsCode": tt.code})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(nodetest.JSONs(got), tt.want) {
				t.Fatalf("got %v\nwant %v", nodetest.JSONs(got), tt.want)
			}
		})
	}
}

func TestInputIsNotAliased(t *testing.T) {
	nested := map[string]any{"k": 1.0}
	items := []nodesdk.Item{{JSON: map[string]any{"nested": nested}}}
	if _, err := exec(t, items, map[string]any{"jsCode": `$input.first().json.nested.k = 99; return [];`}); err != nil {
		t.Fatal(err)
	}
	if nested["k"] != 1.0 {
		t.Fatal("user code changed engine data")
	}
}

func TestSandbox(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"no require", `return [{ t: typeof require }];`},
		{"no process", `return [{ t: typeof process }];`},
		{"no setTimeout", `return [{ t: typeof setTimeout }];`},
		{"no fetch", `return [{ t: typeof fetch }];`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exec(t, nodetest.Items(map[string]any{}), map[string]any{"jsCode": tt.code})
			if err != nil || got[0].JSON["t"] != "undefined" {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
	t.Run("builtins are frozen", func(t *testing.T) {
		_, err := exec(t, nodetest.Items(map[string]any{}), map[string]any{"jsCode": `Array.prototype.polluted = 1; return [];`})
		if err == nil {
			t.Fatal("expected prototype pollution to fail in strict mode")
		}
	})
}

func TestLimits(t *testing.T) {
	items := nodetest.Items(map[string]any{})
	run := func(l Limits, code string) error {
		n := New(l, fixedClock{})
		_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), items, map[string]any{"jsCode": code}))
		return err
	}
	t.Run("timeout", func(t *testing.T) {
		l := limits()
		l.Timeout = 50 * time.Millisecond
		start := time.Now()
		err := run(l, `while (true) {}`)
		if err == nil || !errors.Is(err, errTimeout) || time.Since(start) > 2*time.Second {
			t.Fatalf("err = %v after %v", err, time.Since(start))
		}
	})
	t.Run("recursion", func(t *testing.T) {
		l := limits()
		l.MaxCallStackDepth = 30
		err := run(l, `function f() { return f(); } return f();`)
		if err == nil || !strings.Contains(err.Error(), "JavaScript error") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("output size", func(t *testing.T) {
		l := limits()
		l.MaxOutputBytes = 100
		err := run(l, `return [{ big: "x".repeat(1000) }];`)
		if err == nil || !strings.Contains(err.Error(), "limit is 100") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(30 * time.Millisecond); cancel() }()
		n := newNode()
		_, err := n.Execute(ctx, nodetest.New(n.Describe(), items, map[string]any{"jsCode": `while (true) {}`}))
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestErrors(t *testing.T) {
	items := nodetest.Items(map[string]any{})
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"syntax error", map[string]any{"jsCode": `return [`}, "syntax error"},
		{"thrown error", map[string]any{"jsCode": `throw new Error("boom");`}, "boom"},
		{"reference error", map[string]any{"jsCode": `return undefinedVar;`}, "undefinedVar"},
		{"non-object item", map[string]any{"jsCode": `return [1, 2];`}, "must be an object"},
		{"not serializable", map[string]any{"jsCode": `return [{ f: () => 1 }];`}, "serialized"},
		{"unknown mode", map[string]any{"mode": "forever"}, "unknown mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := exec(t, items, tt.params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestDescriptor(t *testing.T) {
	if err := newNode().Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowScopeThroughEvalExpression(t *testing.T) {
	var asked []string
	eval := func(expr string, _ int) (any, error) {
		asked = append(asked, expr)
		switch expr {
		case `$("Start").first()`:
			return map[string]any{"json": map[string]any{"n": 7.0}}, nil
		case `$("Start").item`:
			return map[string]any{"json": map[string]any{"n": 8.0}}, nil
		case `$("Start").isExecuted`:
			return true, nil
		case `$vars["region"]`:
			return "eu", nil
		case `$vars["missing"]`, `$env["NOPE"]`:
			return nil, nil
		case `$env["HOME_DIR"]`:
			return "/srv", nil
		case `$node["Start"]`:
			return map[string]any{"json": map[string]any{"n": 9.0}}, nil
		case `Object.keys($vars)`:
			return []any{"region"}, nil
		}
		return nil, errors.New("unexpected expression " + expr)
	}
	n := newNode()
	code := `return [{
		first: $("Start").first().json.n, item: $("Start").item.json.n, ran: $("Start").isExecuted,
		region: $vars.region, missing: $vars.missing === undefined, home: $env.HOME_DIR, nope: $env.NOPE === undefined,
		viaNode: $node["Start"].json.n, keys: Object.keys($vars), sha: $fn.sha256("a").length,
	}];`
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"x": 1.0}), map[string]any{"jsCode": code})
	in.EvalFunc = eval
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("%v (asked %v)", err, asked)
	}
	want := map[string]any{"first": 7.0, "item": 8.0, "ran": true, "region": "eu", "missing": true, "home": "/srv", "nope": true,
		"viaNode": 9.0, "keys": []any{"region"}, "sha": 64.0}
	if got := out.Ports[0][0].JSON; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestWorkflowScopeErrorsBecomeJavaScriptErrors(t *testing.T) {
	n := newNode()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), map[string]any{"jsCode": `return [{ v: $("Ghost").first() }];`})
	in.EvalFunc = func(string, int) (any, error) { return nil, errors.New(`node "Ghost" has not run`) }
	_, err := n.Execute(context.Background(), in)
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || !strings.Contains(ne.Message, "Ghost") {
		t.Fatalf("err = %v", err)
	}
}

func TestSnippet(t *testing.T) {
	lim := Limits{Timeout: 200 * time.Millisecond, MaxCallStackDepth: 64, MaxOutputBytes: 1 << 10}
	s, err := NewSnippet(`return {sum: args.a + args.b, h: $fn.sha256("x").length};`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(context.Background(), lim, map[string]any{"a": float64(1), "b": float64(2)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["sum"] != float64(3) || m["h"] != float64(64) {
		t.Fatalf("got %v", got)
	}
	if _, err := NewSnippet(`return (;`); err == nil {
		t.Fatal("syntax error must fail")
	}
	loop, _ := NewSnippet(`while (true) {}`)
	if _, err := loop.Run(context.Background(), lim, nil, nil); err == nil || !strings.Contains(err.Error(), "ran longer") {
		t.Fatalf("timeout err = %v", err)
	}
	thrower, _ := NewSnippet(`throw new Error("bad")`)
	if _, err := thrower.Run(context.Background(), lim, nil, nil); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("throw err = %v", err)
	}
	big, _ := NewSnippet(`return "x".repeat(5000)`)
	if _, err := big.Run(context.Background(), lim, nil, nil); err == nil {
		t.Fatal("oversized result must fail")
	}
	none, _ := NewSnippet(`return`)
	if v, err := none.Run(context.Background(), lim, nil, nil); err != nil || v != nil {
		t.Fatalf("undefined = %v, %v", v, err)
	}
}
