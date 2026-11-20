package executeworkflow

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type fakeRunner struct {
	calls []nodesdk.SubworkflowCall
	res   nodesdk.SubworkflowResult
	err   error
}

func (f *fakeRunner) RunSubworkflow(_ context.Context, c nodesdk.SubworkflowCall) (nodesdk.SubworkflowResult, error) {
	f.calls = append(f.calls, c)
	return f.res, f.err
}

func input(n nodesdk.Node, params map[string]any, depth int) *nodetest.Input {
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"a": 1.0}), params)
	in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex_parent", Depth: depth}
	return in
}

func TestWaitForResultReturnsChildOutput(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{ExecutionID: "ex_c", Status: "success", Items: nodetest.Items(map[string]any{"out": true})}}
	n := New(r, 3)
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	out := nodetest.Run(t, n, input(n, map[string]any{"workflowId": "wf_1"}, 1))
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, []map[string]any{{"out": true}}) {
		t.Fatalf("got %v", got)
	}
	c := r.calls[0]
	if c.WorkflowID != "wf_1" || c.ParentExecutionID != "ex_parent" || c.Depth != 2 || !c.WaitForResult || len(c.Items) != 1 {
		t.Fatalf("call = %+v", c)
	}
}

func TestFireAndForgetPassesInputThrough(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{ExecutionID: "ex_c", Status: "queued"}}
	n := New(r, 3)
	out := nodetest.Run(t, n, input(n, map[string]any{"workflowId": "wf_1", "waitForResult": false}, 0))
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, []map[string]any{{"a": 1.0}}) {
		t.Fatalf("got %v", got)
	}
	if r.calls[0].WaitForResult {
		t.Fatal("must not wait")
	}
}

func TestDepthLimit(t *testing.T) {
	r := &fakeRunner{}
	n := New(r, 2)
	_, err := n.Execute(context.Background(), input(n, map[string]any{"workflowId": "wf_1"}, 2))
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || !strings.Contains(ne.Message, "depth 3") || len(r.calls) != 0 {
		t.Fatalf("err = %v, calls = %d", err, len(r.calls))
	}
	r.res = nodesdk.SubworkflowResult{Status: "success"}
	if _, err := n.Execute(context.Background(), input(n, map[string]any{"workflowId": "wf_1"}, 1)); err != nil {
		t.Fatalf("depth 2 of 2 is allowed: %v", err)
	}
}

func TestChildFailureFailsTheNode(t *testing.T) {
	r := &fakeRunner{res: nodesdk.SubworkflowResult{ExecutionID: "ex_c", Status: "error", Error: "boom"}}
	n := New(r, 3)
	_, err := n.Execute(context.Background(), input(n, map[string]any{"workflowId": "wf_1"}, 0))
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || !strings.Contains(ne.Message, "ex_c") || ne.Description != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerFailureFailsTheNode(t *testing.T) {
	r := &fakeRunner{err: errors.New("no such workflow")}
	n := New(r, 3)
	_, err := n.Execute(context.Background(), input(n, map[string]any{"workflowId": "wf_x"}, 0))
	var ne *nodesdk.NodeError
	if !errors.As(err, &ne) || !strings.Contains(ne.Description, "no such workflow") {
		t.Fatalf("err = %v", err)
	}
}
