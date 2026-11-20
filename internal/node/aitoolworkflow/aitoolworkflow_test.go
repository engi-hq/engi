package aitoolworkflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/aitoolworkflow"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeRunner struct {
	call nodesdk.SubworkflowCall
	res  nodesdk.SubworkflowResult
	err  error
}

func (f *fakeRunner) RunSubworkflow(_ context.Context, c nodesdk.SubworkflowCall) (nodesdk.SubworkflowResult, error) {
	f.call = c
	return f.res, f.err
}

func tool(t *testing.T, r *fakeRunner, depth int) (port.Tool, error) {
	t.Helper()
	n := aitoolworkflow.New(aitoolworkflow.Config{Runner: r, MaxDepth: 3})
	in := nodetest.New(n.Describe(), nil, map[string]any{"name": "lookup", "workflowId": "wf_1",
		"parameters": `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`})
	in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_1", Depth: depth}
	v, err := n.Supply(context.Background(), in)
	if err != nil {
		return nil, err
	}
	tools, _ := v.(port.ToolProvider).Tools(context.Background())
	return tools[0], nil
}

func TestDescriptor(t *testing.T) {
	d := aitoolworkflow.New(aitoolworkflow.Config{}).Describe()
	if err := d.Validate(); err != nil || !d.IsSubNode() {
		t.Fatalf("invalid: %v", err)
	}
}

func TestCallRunsChildWithArgsAsItem(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{Status: "success", Items: []nodesdk.Item{{JSON: map[string]any{"name": "Ann"}}}}}
	tl, err := tool(t, r, 1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tl.Call(context.Background(), json.RawMessage(`{"id":"7"}`))
	if err != nil || res.IsError || res.Content != `{"name":"Ann"}` {
		t.Fatalf("result = %+v, %v", res, err)
	}
	c := r.call
	if c.WorkflowID != "wf_1" || c.ParentExecutionID != "ex_1" || c.Depth != 2 || !c.WaitForResult || c.Items[0].JSON["id"] != "7" {
		t.Fatalf("call = %+v", c)
	}
}

func TestManyItemsBecomeArray(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{Status: "success", Items: []nodesdk.Item{{JSON: map[string]any{"a": 1}}, {JSON: map[string]any{"a": 2}}}}}
	tl, _ := tool(t, r, 0)
	res, _ := tl.Call(context.Background(), json.RawMessage(`{"id":"7"}`))
	if res.Content != `[{"a":1},{"a":2}]` {
		t.Fatalf("content = %s", res.Content)
	}
}

func TestFailuresAreResults(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{Status: "error", Error: "boom"}}
	tl, _ := tool(t, r, 0)
	res, err := tl.Call(context.Background(), json.RawMessage(`{"id":"7"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "boom") {
		t.Fatalf("result = %+v, %v", res, err)
	}
	r.err = errors.New("no such workflow")
	res, err = tl.Call(context.Background(), json.RawMessage(`{"id":"7"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "no such workflow") {
		t.Fatalf("start failure = %+v, %v", res, err)
	}
	res, _ = tl.Call(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("missing arg must be an error result")
	}
}

func TestDepthLimitAndUnavailable(t *testing.T) {
	if _, err := tool(t, &fakeRunner{}, 3); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth err = %v", err)
	}
	n := aitoolworkflow.New(aitoolworkflow.Config{})
	if _, err := n.Supply(context.Background(), nodetest.New(n.Describe(), nil, nil)); !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
