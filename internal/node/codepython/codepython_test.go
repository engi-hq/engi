package codepython

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeRunner struct {
	got    Job
	result Result
	err    error
	run    func(ctx context.Context, job Job)
}

func (f *fakeRunner) Run(ctx context.Context, job Job) (Result, error) {
	f.got = job
	if f.run != nil {
		f.run(ctx, job)
	}
	return f.result, f.err
}

func testLimits() Limits {
	return Limits{Timeout: 3 * time.Second, MaxOutputBytes: 1234, AllowedModules: []string{"json"}}
}

func item(v any) nodesdk.Item { return nodesdk.Item{JSON: map[string]any{"v": v}} }

func TestExecuteAllItems(t *testing.T) {
	r := &fakeRunner{result: Result{Items: []nodesdk.Item{item(1), item(2)}}}
	n := New(r, testLimits())
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}), nil)
	in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", RunIndex: 3}
	out, err := n.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Ports) != 1 || len(out.Ports[0]) != 2 {
		t.Fatalf("ports: %+v", out.Ports)
	}
	if len(out.Ports[0][0].PairedItem) != 0 {
		t.Errorf("all-items mode must not invent pairedItem: %+v", out.Ports[0][0].PairedItem)
	}
	g := r.got
	if g.Mode != ModeAll || g.Code != defaultCode || g.Execution.ExecutionID != "ex_1" || g.Execution.RunIndex != 3 ||
		g.Timeout != 3*time.Second || g.MaxOutputBytes != 1234 || !reflect.DeepEqual(g.AllowedModules, []string{"json"}) ||
		len(g.Items) != 1 {
		t.Errorf("job: %+v", g)
	}
}

func TestExecutePerItem(t *testing.T) {
	paired := nodesdk.Item{JSON: map[string]any{"keep": true}, PairedItem: []nodesdk.PairedItem{{Input: 0, Item: 2}}}
	tests := []struct {
		name    string
		perItem [][]nodesdk.Item
		wantErr bool
		want    []nodesdk.PairedItem
		wantLen int
	}{
		{"each result is paired with its source", [][]nodesdk.Item{{item(1)}, {item(2), item(3)}, {item(4)}}, false,
			[]nodesdk.PairedItem{{Input: 0, Item: 0}, {Input: 0, Item: 1}, {Input: 0, Item: 1}, {Input: 0, Item: 2}}, 4},
		{"nil drops the item", [][]nodesdk.Item{nil, {item(2)}, nil}, false, []nodesdk.PairedItem{{Input: 0, Item: 1}}, 1},
		{"existing pairedItem is kept", [][]nodesdk.Item{nil, nil, {paired}}, false, []nodesdk.PairedItem{{Input: 0, Item: 2}}, 1},
		{"all dropped", [][]nodesdk.Item{nil, nil, nil}, false, nil, 0},
		{"wrong count", [][]nodesdk.Item{nil}, true, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{result: Result{PerItem: tt.perItem}}
			n := New(r, testLimits())
			in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}, map[string]any{}, map[string]any{}),
				map[string]any{"mode": ModeEach})
			out, err := n.Execute(context.Background(), in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			got := out.Ports[0]
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			var pairs []nodesdk.PairedItem
			for _, it := range got {
				pairs = append(pairs, it.PairedItem...)
			}
			if !reflect.DeepEqual(pairs, tt.want) {
				t.Errorf("pairedItem = %v, want %v", pairs, tt.want)
			}
			if r.got.Mode != ModeEach {
				t.Errorf("mode = %q", r.got.Mode)
			}
		})
	}
}

func TestExecuteErrors(t *testing.T) {
	idx := 1
	itemErr := &nodesdk.NodeError{Message: "ValueError: bad", ItemIndex: &idx}
	boom := errors.New("boom")
	tests := []struct {
		name   string
		params map[string]any
		runErr error
		check  func(t *testing.T, err error)
	}{
		{"item index is kept", map[string]any{"mode": ModeEach}, itemErr, func(t *testing.T, err error) {
			var ne *nodesdk.NodeError
			if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 1 || ne.Message != "ValueError: bad" {
				t.Errorf("err = %#v", err)
			}
		}},
		{"other errors pass through", nil, boom, func(t *testing.T, err error) {
			if !errors.Is(err, boom) {
				t.Errorf("err = %v", err)
			}
		}},
		{"unknown mode", map[string]any{"mode": "sideways"}, nil, func(t *testing.T, err error) {
			if err == nil {
				t.Error("want error")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := New(&fakeRunner{err: tt.runErr}, testLimits())
			in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}, map[string]any{}), tt.params)
			_, err := n.Execute(context.Background(), in)
			tt.check(t, err)
		})
	}
}

func TestCallbacksUseInput(t *testing.T) {
	var logged []string
	r := &fakeRunner{run: func(ctx context.Context, job Job) {
		v, err := job.Eval(ctx, "$json.x", 2)
		if err != nil || v != "evaluated" {
			t.Errorf("eval = %v, %v", v, err)
		}
		job.Log("hello")
		logged = append(logged, "hello")
	}}
	n := New(r, testLimits())
	in := nodetest.New(n.Describe(), nil, nil)
	var gotExpr string
	var gotIndex int
	in.EvalFunc = func(expression string, itemIndex int) (any, error) {
		gotExpr, gotIndex = expression, itemIndex
		return "evaluated", nil
	}
	if _, err := n.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if gotExpr != "$json.x" || gotIndex != 2 || len(logged) != 1 {
		t.Errorf("expr %q index %d logged %v", gotExpr, gotIndex, logged)
	}
}

func TestDescribe(t *testing.T) {
	d := New(&fakeRunner{}, testLimits()).Describe()
	if d.Type != "engi.code.python" || d.Version != 1 || d.Group != nodesdk.GroupTransform || d.Icon != "code" {
		t.Errorf("descriptor: %+v", d)
	}
	if len(d.Properties) != 2 || d.Properties[1].Name != "pythonCode" || !d.Properties[1].Required {
		t.Errorf("properties: %+v", d.Properties)
	}
}
