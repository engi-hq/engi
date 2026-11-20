package pluginnode_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

type fakeCaller struct {
	got pluginproto.ExecuteParams
	cb  pluginnode.Callbacks
	res pluginproto.ExecuteResult
	err error
	run func(cb pluginnode.Callbacks)
}

func (f *fakeCaller) Execute(_ context.Context, p pluginproto.ExecuteParams, cb pluginnode.Callbacks) (pluginproto.ExecuteResult, error) {
	f.got, f.cb = p, cb
	if f.run != nil {
		f.run(cb)
	}
	return f.res, f.err
}

func desc() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: "acme.test.node", Version: 2, DisplayName: "T", Group: nodesdk.GroupAction,
		Inputs:  []nodesdk.PortSpec{{Name: "main"}},
		Outputs: []nodesdk.PortSpec{{Name: "a"}, {Name: "b"}},
		Properties: []nodesdk.Property{
			{Name: "field", Type: nodesdk.PropString, Default: "x"},
			{Name: "need", Type: nodesdk.PropString, Required: true},
			{Name: "info", Type: nodesdk.PropNotice},
		},
		Credentials: []nodesdk.CredentialRef{{Name: "api", Type: "acme.apiKey"}, {Name: "opt", Type: "acme.other"}},
	}
}

func newNode(c *fakeCaller, maxBlob int64) nodesdk.Node {
	return pluginnode.New(desc(), c, pluginnode.Options{MaxBlobBytes: maxBlob, Rand: bytes.NewReader(bytes.Repeat([]byte{0xab}, 64))})
}

func okResult() pluginproto.ExecuteResult {
	return pluginproto.ExecuteResult{Outputs: [][]pluginproto.Item{{{JSON: map[string]any{"a": 1.0}}}, {}}}
}

func TestExecuteBuildsParams(t *testing.T) {
	tests := []struct {
		name      string
		items     int
		wantParam int
	}{
		{"two items", 2, 2},
		{"zero items still one entry", 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := make([]map[string]any, tt.items)
			for i := range objs {
				objs[i] = map[string]any{"n": float64(i)}
			}
			in := nodetest.New(desc(), nodetest.Items(objs...), map[string]any{"field": "f"})
			in.Info = nodesdk.ExecutionInfo{ExecutionID: "ex1", Attempt: 1}
			in.Credentials = map[string]nodesdk.Credential{"api": {Type: "acme.apiKey", ID: "c1", Version: 3, Data: map[string]any{"key": "k"}}}
			c := &fakeCaller{res: okResult()}
			out, err := newNode(c, 0).Execute(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Ports) != 2 {
				t.Fatalf("ports = %d", len(out.Ports))
			}
			p := c.got
			if p.CallID != strings.Repeat("ab", 16) {
				t.Errorf("CallID = %q", p.CallID)
			}
			if p.Node != (pluginproto.NodeRef{Type: "acme.test.node", Version: 2}) || p.Execution.ExecutionID != "ex1" {
				t.Errorf("node/execution = %+v %+v", p.Node, p.Execution)
			}
			if len(p.Inputs) != 1 || len(p.Inputs[0]) != tt.items {
				t.Errorf("inputs = %+v", p.Inputs)
			}
			if len(p.Params) != tt.wantParam || len(p.ParamErrors) != tt.wantParam {
				t.Fatalf("params = %d, paramErrors = %d", len(p.Params), len(p.ParamErrors))
			}
			for i := range p.Params {
				if p.Params[i]["field"] != "f" {
					t.Errorf("params[%d] = %v", i, p.Params[i])
				}
				if _, ok := p.Params[i]["info"]; ok {
					t.Error("notice must be skipped")
				}
				if _, ok := p.Params[i]["need"]; ok {
					t.Error("failed param must be omitted")
				}
				if !strings.Contains(p.ParamErrors[i]["need"], "missing") {
					t.Errorf("paramErrors[%d] = %v", i, p.ParamErrors[i])
				}
			}
			if cr := p.Credentials["api"]; cr.ID != "c1" || cr.Version != 3 || cr.Data["key"] != "k" {
				t.Errorf("credentials = %+v", p.Credentials)
			}
			if _, ok := p.CredentialErrors["opt"]; !ok {
				t.Errorf("credentialErrors = %v", p.CredentialErrors)
			}
		})
	}
}

func TestExecuteResults(t *testing.T) {
	nodeErr := &pluginproto.Error{Code: pluginproto.CodeNodeError, Message: "boom", Data: []byte(`{"description":"d","httpStatus":502}`)}
	tests := []struct {
		name    string
		res     pluginproto.ExecuteResult
		err     error
		check   func(t *testing.T, err error)
		wantErr bool
	}{
		{name: "port count mismatch", res: pluginproto.ExecuteResult{Outputs: [][]pluginproto.Item{{}}}, wantErr: true,
			check: func(t *testing.T, err error) {
				if !strings.Contains(err.Error(), "1 outputs") {
					t.Errorf("err = %v", err)
				}
			}},
		{name: "node error", err: nodeErr, wantErr: true,
			check: func(t *testing.T, err error) {
				var ne *nodesdk.NodeError
				if !errors.As(err, &ne) || ne.Message != "boom" || ne.HTTPStatus != 502 || ne.Description != "d" {
					t.Errorf("err = %#v", err)
				}
			}},
		{name: "other error wrapped", err: errors.New("pipe closed"), wantErr: true,
			check: func(t *testing.T, err error) {
				if !strings.Contains(err.Error(), "pipe closed") || !strings.Contains(err.Error(), "acme.test.node") {
					t.Errorf("err = %v", err)
				}
			}},
		{name: "ok", res: okResult()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &fakeCaller{res: tt.res, err: tt.err}
			_, err := newNode(c, 0).Execute(context.Background(), nodetest.New(desc(), nil, nil))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if tt.check != nil {
				tt.check(t, err)
			}
		})
	}
}

func TestCallbacks(t *testing.T) {
	blobs := nodetest.NewMemoryBlobs()
	in := nodetest.New(desc(), nil, nil)
	in.Blobs = blobs
	in.EvalFunc = func(expr string, idx int) (any, error) { return expr + "!", nil }
	ctx := context.Background()

	var ref pluginproto.BinaryRef
	var back []byte
	var evalv any
	var tooBig error
	c := &fakeCaller{res: okResult()}
	c.run = func(cb pluginnode.Callbacks) {
		var err error
		if ref, err = cb.WriteBlob(ctx, "n.txt", "text/plain", []byte("hello")); err != nil {
			t.Error(err)
		}
		if back, err = cb.ReadBlob(ctx, ref); err != nil {
			t.Error(err)
		}
		evalv, _ = cb.Eval(ctx, "1+1", 0)
		big, _ := cb.WriteBlob(ctx, "big", "x", []byte("0123456789abcdef"))
		_, tooBig = cb.ReadBlob(ctx, big)
		cb.Log("warn", "hi", map[string]any{"k": 1})
	}
	if _, err := newNode(c, 8).Execute(ctx, in); err != nil {
		t.Fatal(err)
	}
	if string(back) != "hello" || ref.Name != "n.txt" || ref.Size != 5 {
		t.Errorf("blob round trip: %q %+v", back, ref)
	}
	if evalv != "1+1!" {
		t.Errorf("eval = %v", evalv)
	}
	if tooBig == nil {
		t.Error("blob above the limit must be refused")
	}
}
