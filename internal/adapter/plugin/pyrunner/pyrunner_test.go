package pyrunner

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/codepython"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

var testModules = []string{"json", "math", "urllib.parse"}

func newRunner(t *testing.T) *Runner {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	return New(Config{Interpreter: path})
}

func job(code, mode string, items ...map[string]any) codepython.Job {
	its := make([]nodesdk.Item, len(items))
	for i, m := range items {
		its[i] = nodesdk.Item{JSON: m}
	}
	return codepython.Job{
		Code: code, Mode: mode, Items: its, AllowedModules: testModules,
		Timeout: 10 * time.Second, MaxOutputBytes: 1 << 20,
		Execution: nodesdk.ExecutionInfo{ExecutionID: "ex_1", WorkflowID: "wf_1", WorkflowName: "demo", Mode: "manual", RunIndex: 2},
		Eval:      func(context.Context, string, int) (any, error) { return nil, errors.New("no eval") },
		Log:       func(string) {},
	}
}

func jsons(items []nodesdk.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = it.JSON
	}
	return out
}

func nodeErr(t *testing.T, err error) *nodesdk.NodeError {
	t.Helper()
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) {
		t.Fatalf("want NodeError, got %T: %v", err, err)
	}
	return ne
}

func TestAllItems(t *testing.T) {
	r := newRunner(t)
	in := []map[string]any{{"n": 1.0}, {"n": 2.0}, {"n": 3.0}}
	tests := []struct {
		name string
		code string
		want []map[string]any
	}{
		{"pass through", "return items", in},
		{"map to items", `return [{"json": {"n2": i["json"]["n"] * 2}} for i in items]`,
			[]map[string]any{{"n2": 2.0}, {"n2": 4.0}, {"n2": 6.0}}},
		{"plain dicts", `return [{"a": 1}, {"a": 2}]`, []map[string]any{{"a": 1.0}, {"a": 2.0}}},
		{"single dict", `return {"total": len(items)}`, []map[string]any{{"total": 3.0}}},
		{"no return", `x = 1`, nil},
		{"None", `return None`, nil},
		{"empty list", `return []`, nil},
		{"scope", `return {"e": execution["id"], "m": execution["mode"], "w": workflow["name"], "wid": workflow["id"], "r": run_index, "i": item_index, "item": item, "data": json}`,
			[]map[string]any{{"e": "ex_1", "m": "manual", "w": "demo", "wid": "wf_1", "r": 2.0, "i": 0.0, "item": nil, "data": map[string]any{"n": 1.0}}}},
		{"allowed import and helper function", "import math\ndef f(x):\n    return math.sqrt(x)\nreturn {'v': f(16)}", []map[string]any{{"v": 4.0}}},
		{"from import", "from urllib.parse import quote\nreturn {'q': quote('a b')}", []map[string]any{{"q": "a%20b"}}},
		{"from package import submodule", "from urllib import parse\nreturn {'q': parse.quote('a b')}", []map[string]any{{"q": "a%20b"}}},
		{"json module", "import json\nreturn {'j': json.dumps({'a': 1})}", []map[string]any{{"j": `{"a": 1}`}}},
		{"class statement", "class A:\n    x = 3\nreturn {'x': A().x}", []map[string]any{{"x": 3.0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := r.Run(context.Background(), job(tt.code, codepython.ModeAll, in...))
			if err != nil {
				t.Fatal(err)
			}
			if got := jsons(res.Items); !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// In all-items mode json is the first item's data, as $json is in the
// JavaScript Code node; with no input it is an empty dict.
func TestAllItemsJSONWithoutInput(t *testing.T) {
	r := newRunner(t)
	res, err := r.Run(context.Background(), job(`return {"keys": len(json), "item": item}`, codepython.ModeAll))
	if err != nil {
		t.Fatal(err)
	}
	if got := jsons(res.Items); !reflect.DeepEqual(got, []map[string]any{{"keys": 0.0, "item": nil}}) {
		t.Errorf("got %v", got)
	}
}

func TestPerItem(t *testing.T) {
	r := newRunner(t)
	in := []map[string]any{{"email": "a@x.io"}, {"email": ""}, {"email": "c@y.io"}}
	code := `
if not json["email"]:
    return None
if item_index == 2:
    return [{"idx": item_index}, {"idx": item_index, "dup": True}]
return {"domain": json["email"].split("@")[1], "idx": item_index}
`
	res, err := r.Run(context.Background(), job(code, codepython.ModeEach, in...))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PerItem) != 3 || res.PerItem[1] != nil {
		t.Fatalf("per item: %+v", res.PerItem)
	}
	if got := jsons(res.PerItem[0]); !reflect.DeepEqual(got, []map[string]any{{"domain": "x.io", "idx": 0.0}}) {
		t.Errorf("item 0: %v", got)
	}
	if got := jsons(res.PerItem[2]); !reflect.DeepEqual(got, []map[string]any{{"idx": 2.0}, {"idx": 2.0, "dup": true}}) {
		t.Errorf("item 2: %v", got)
	}
}

func TestExprRoundTripAndPrint(t *testing.T) {
	r := newRunner(t)
	var mu sync.Mutex
	var logs []string
	type call struct {
		expr string
		idx  int
	}
	var calls []call
	j := job(`
print("hello", item_index)
print("multi\nline")
a = expr("$json.x")
b = expr("$('Other').first().json.y", 7)
return {"a": a, "b": b}
`, codepython.ModeEach, map[string]any{}, map[string]any{})
	j.Log = func(m string) { mu.Lock(); logs = append(logs, m); mu.Unlock() }
	j.Eval = func(_ context.Context, e string, idx int) (any, error) {
		calls = append(calls, call{e, idx})
		return map[string]any{"e": e, "i": float64(idx)}, nil
	}
	res, err := r.Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsons(res.PerItem[1]); got[0]["a"].(map[string]any)["i"] != 1.0 || got[0]["b"].(map[string]any)["i"] != 7.0 {
		t.Errorf("results: %v", got)
	}
	wantCalls := []call{{"$json.x", 0}, {"$('Other').first().json.y", 7}, {"$json.x", 1}, {"$('Other').first().json.y", 7}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls: %v", calls)
	}
	wantLogs := []string{"hello 0", "multi", "line", "hello 1", "multi", "line"}
	if !reflect.DeepEqual(logs, wantLogs) {
		t.Errorf("logs: %q", logs)
	}
}

func TestExprFailure(t *testing.T) {
	r := newRunner(t)
	j := job(`
try:
    expr("boom")
except ExprError as e:
    return {"caught": str(e)}
`, codepython.ModeAll)
	j.Eval = func(context.Context, string, int) (any, error) {
		return nil, &nodesdk.NodeError{Message: "bad expression"}
	}
	res, err := r.Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Items[0].JSON["caught"].(string); !strings.Contains(got, "bad expression") {
		t.Errorf("caught %q", got)
	}
}

func TestRefusedCode(t *testing.T) {
	r := newRunner(t)
	tests := []struct {
		name string
		code string
		want []string
	}{
		{"blocked import with line", "x = 1\nimport os\nreturn items", []string{"line 2", "import of 'os'"}},
		{"submodule of blocked", "import os.path", []string{"line 1", "os.path"}},
		{"parent of allowed submodule", "import urllib", []string{"import of 'urllib'"}},
		{"from blocked", "from subprocess import run", []string{"subprocess"}},
		{"relative import", "from . import x", []string{"relative imports"}},
		{"dunder import name", "__import__('os')", []string{"__import__"}},
		{"importlib", "import importlib", []string{"importlib"}},
		{"eval", "eval('1')", []string{"'eval'"}},
		{"exec", "exec('1')", []string{"'exec'"}},
		{"open", "open('/etc/passwd')", []string{"'open'"}},
		{"getattr", "getattr(items, 'x')", []string{"'getattr'"}},
		{"globals", "globals()", []string{"'globals'"}},
		{"dunder attribute", "x = ().__class__.__bases__", []string{"line 1", "attribute '__class__'"}},
		{"subclasses", "return [].__class__.__base__.__subclasses__()", []string{"__subclasses__"}},
		{"frame attribute", "def g():\n    yield 1\nreturn g().gi_frame", []string{"line 3", "gi_frame"}},
		{"syntax error", "return (", []string{"SyntaxError", "line 1"}},
		{"several problems", "import os\nimport sys", []string{"line 1", "line 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), job(tt.code, codepython.ModeAll, map[string]any{}))
			if err == nil {
				t.Fatal("want error")
			}
			ne := nodeErr(t, err)
			for _, w := range tt.want {
				if !strings.Contains(ne.Message, w) {
					t.Errorf("message %q lacks %q", ne.Message, w)
				}
			}
		})
	}
}

func TestRuntimeGuards(t *testing.T) {
	r := newRunner(t)
	// Bypass attempts the static check cannot see must still fail at run time.
	tests := []struct {
		name string
		code string
		want string
	}{
		{"builtins are restricted", "return {'v': (lambda: open)()}", "ForbiddenCode"},
		{"no getattr builtin", "n = 'get' + 'attr'\nreturn dir", "NameError"},
		{"print only to log", "print('x', file=None)\nreturn None", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), job(tt.code, codepython.ModeAll))
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(nodeErr(t, err).Message, tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUserException(t *testing.T) {
	r := newRunner(t)
	code := "def f(x):\n    return 1 / x\n\nvals = [1, 0]\nreturn {'v': f(vals[1])}"
	_, err := r.Run(context.Background(), job(code, codepython.ModeAll, map[string]any{}))
	ne := nodeErr(t, err)
	if ne.Message != "ZeroDivisionError: division by zero" {
		t.Errorf("message %q", ne.Message)
	}
	for _, w := range []string{"line 2, in f: return 1 / x", "line 5: return {'v': f(vals[1])}"} {
		if !strings.Contains(ne.Description, w) {
			t.Errorf("description %q lacks %q", ne.Description, w)
		}
	}
	if strings.Contains(ne.Description, "runner.py") || strings.Contains(ne.Description, "<string>") {
		t.Errorf("description leaks runner frames: %q", ne.Description)
	}
	if ne.ItemIndex != nil {
		t.Errorf("all-items errors carry no item index")
	}
}

func TestUserExceptionItemIndex(t *testing.T) {
	r := newRunner(t)
	code := "if item_index == 1:\n    raise ValueError('bad item')\nreturn {}"
	_, err := r.Run(context.Background(), job(code, codepython.ModeEach, map[string]any{}, map[string]any{}))
	ne := nodeErr(t, err)
	if ne.Message != "ValueError: bad item" || ne.ItemIndex == nil || *ne.ItemIndex != 1 {
		t.Errorf("got %+v", ne)
	}
}

func TestBadReturnShapes(t *testing.T) {
	r := newRunner(t)
	tests := []struct {
		name, code, mode, want string
	}{
		{"list of non-dicts", "return [1, 2]", codepython.ModeAll, "returned item 0 must be a dict, got int"},
		{"string", "return 'x'", codepython.ModeAll, "must be a dict, got str"},
		{"not serializable", "return {'a': object()}", codepython.ModeAll, "not JSON serializable"},
		{"nan", "return {'a': float('nan')}", codepython.ModeAll, "ValueError"},
		{"per item bad", "return 5", codepython.ModeEach, "must be a dict, got int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Run(context.Background(), job(tt.code, tt.mode, map[string]any{}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTimeoutKillsInfiniteLoop(t *testing.T) {
	r := newRunner(t)
	j := job("while True:\n    pass", codepython.ModeAll)
	j.Timeout = 500 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), j)
	took := time.Since(start)
	if err == nil || !strings.Contains(nodeErr(t, err).Message, "ran longer than 500ms") {
		t.Fatalf("err = %v", err)
	}
	if took > j.Timeout+3*time.Second {
		t.Errorf("took %s", took)
	}
}

func TestContextCancel(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := r.Run(ctx, job("while True:\n    pass", codepython.ModeAll))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancel was not prompt")
	}
}

func TestOutputCap(t *testing.T) {
	r := newRunner(t)
	j := job("return {'big': 'x' * 5000}", codepython.ModeAll)
	j.MaxOutputBytes = 1000
	_, err := r.Run(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "limit is 1000") {
		t.Errorf("err = %v", err)
	}
}

// A runner that ignores its own cap is still bounded by the connection limit.
func TestOutputCapBackstop(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	r := New(Config{Interpreter: "python3"})
	j := job("return {'big': 'x' * 200000}", codepython.ModeAll)
	j.MaxOutputBytes = 10
	_, err := r.Run(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v", err)
	}
}

func TestEnvironmentIsEmpty(t *testing.T) {
	r := newRunner(t)
	t.Setenv("ENGI_SECRET_FOR_TEST", "hunter2")
	j := job("import os\nreturn {'env': dict(os.environ)}", codepython.ModeAll)
	j.AllowedModules = []string{"os"}
	res, err := r.Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := res.Items[0].JSON["env"].(map[string]any)
	if _, leaked := env["ENGI_SECRET_FOR_TEST"]; leaked {
		t.Errorf("host environment leaked: %v", env)
	}
	if _, leaked := env["PATH"]; leaked {
		t.Errorf("PATH leaked: %v", env)
	}
}

func TestInterpreterNotFound(t *testing.T) {
	r := New(Config{Interpreter: "definitely-not-a-python"})
	_, err := r.Run(context.Background(), job("return items", codepython.ModeAll))
	ne := nodeErr(t, err)
	if !strings.Contains(ne.Message, "definitely-not-a-python") || !strings.Contains(ne.Description, "nodes.code.python.interpreter") {
		t.Errorf("got %+v", ne)
	}
}

func TestChildCrashReportsStderr(t *testing.T) {
	// "false" exists on unix and exits without speaking the protocol.
	path, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false binary")
	}
	_, err = New(Config{Interpreter: path}).Run(context.Background(), job("return items", codepython.ModeAll))
	if err == nil || !strings.Contains(err.Error(), "Python process failed") {
		t.Errorf("err = %v", err)
	}
}

func TestInvalidJob(t *testing.T) {
	r := New(Config{Interpreter: "python3"})
	j := job("return items", codepython.ModeAll)
	j.Timeout = 0
	if _, err := r.Run(context.Background(), j); err == nil {
		t.Error("zero timeout accepted")
	}
}

func TestRepeatedRuns(t *testing.T) {
	r := newRunner(t)
	// Many runs in a row must not leave processes or goroutines behind; the
	// race detector and the process table would show a leak as a hang.
	for i := 0; i < 5; i++ {
		if _, err := r.Run(context.Background(), job("return items", codepython.ModeAll, map[string]any{"i": float64(i)})); err != nil {
			t.Fatal(err)
		}
	}
}
