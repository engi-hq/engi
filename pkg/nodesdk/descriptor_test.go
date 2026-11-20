package nodesdk_test

import (
	"strings"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestDescriptorValidate(t *testing.T) {
	valid := func() nodesdk.Descriptor {
		return nodesdk.Descriptor{
			Type: "test.node", Version: 1,
			Properties: []nodesdk.Property{{Name: "a", Type: nodesdk.PropString}},
		}
	}
	tests := []struct {
		name    string
		mutate  func(*nodesdk.Descriptor)
		wantErr string
	}{
		{"valid", func(*nodesdk.Descriptor) {}, ""},
		{"no type", func(d *nodesdk.Descriptor) { d.Type = "" }, "type must not be empty"},
		{"version zero", func(d *nodesdk.Descriptor) { d.Version = 0 }, "version"},
		{"bad policy", func(d *nodesdk.Descriptor) { d.MergePolicy = "sometimes" }, "merge policy"},
		{"unnamed property", func(d *nodesdk.Descriptor) { d.Properties[0].Name = "" }, "name must not be empty"},
		{"duplicate property", func(d *nodesdk.Descriptor) {
			d.Properties = append(d.Properties, d.Properties[0])
		}, "duplicate"},
		{"bad property type", func(d *nodesdk.Descriptor) { d.Properties[0].Type = "blob" }, "unknown type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid()
			tt.mutate(&d)
			err := d.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestOutputKindDefaultsToMain(t *testing.T) {
	d := nodesdk.Descriptor{Outputs: []nodesdk.PortSpec{{Name: "x"}, {Name: "tool", Kind: "ai.tool"}}}
	if d.OutputKind(0) != nodesdk.PortMain || d.OutputKind(1) != "ai.tool" || d.OutputKind(5) != nodesdk.PortMain {
		t.Fatal("OutputKind defaults wrong")
	}
}

func TestNodeErrorMessage(t *testing.T) {
	if got := (&nodesdk.NodeError{Message: "m"}).Error(); got != "m" {
		t.Fatalf("got %q", got)
	}
}

func TestPortKinds(t *testing.T) {
	for k, want := range map[nodesdk.PortKind]struct{ sub, valid bool }{
		nodesdk.PortMain: {false, true}, nodesdk.PortAIModel: {true, true}, nodesdk.PortAITool: {true, true},
		nodesdk.PortAIMemory: {true, true}, nodesdk.PortAIEmbedding: {true, true}, nodesdk.PortAIVectorStore: {true, true},
		nodesdk.PortAITextSplitter: {true, true}, "ai.custom": {true, true},
		"ai.": {false, false}, "": {false, false}, "aimodel": {false, false}, "other": {false, false},
	} {
		if k.IsSub() != want.sub || k.Valid() != want.valid {
			t.Errorf("%q: IsSub=%v Valid=%v, want %+v", k, k.IsSub(), k.Valid(), want)
		}
	}
}

func TestDescriptorSubPorts(t *testing.T) {
	parent := nodesdk.Descriptor{Type: "t.parent", Version: 1,
		Inputs: []nodesdk.PortSpec{
			{Kind: nodesdk.PortAIModel, Required: true, MaxConnections: 1},
			{Kind: nodesdk.PortMain},
			{Kind: nodesdk.PortAITool},
			{Kind: nodesdk.PortAITool, MaxConnections: 2},
		},
		Outputs: []nodesdk.PortSpec{{}}}
	if err := parent.Validate(); err != nil {
		t.Fatal(err)
	}
	if parent.IsSubNode() {
		t.Error("a parent with only a main output is not a sub-node")
	}
	if n := parent.PortCount(true, nodesdk.PortMain); n != 1 {
		t.Errorf("main inputs = %d", n)
	}
	if p, ok := parent.PortOfKind(true, nodesdk.PortAITool, 1); !ok || p.MaxConnections != 2 {
		t.Errorf("second tool input = %+v %v", p, ok)
	}
	if _, ok := parent.PortOfKind(true, nodesdk.PortAITool, 2); ok {
		t.Error("only two tool inputs exist")
	}
	if p, ok := parent.PortOfKind(true, nodesdk.PortMain, 0); !ok || p.Kind != nodesdk.PortMain {
		t.Errorf("main ordinal 0 = %+v %v", p, ok)
	}

	subNode := nodesdk.Descriptor{Type: "t.sub", Version: 1, Outputs: []nodesdk.PortSpec{{Kind: nodesdk.PortAITool}}}
	if !subNode.IsSubNode() || subNode.Validate() != nil {
		t.Errorf("sub-node: IsSubNode=%v err=%v", subNode.IsSubNode(), subNode.Validate())
	}
	for name, mutate := range map[string]func(*nodesdk.Descriptor){
		"unknown kind": func(d *nodesdk.Descriptor) { d.Inputs = []nodesdk.PortSpec{{Kind: "weird"}} },
		"negative max": func(d *nodesdk.Descriptor) {
			d.Inputs = []nodesdk.PortSpec{{Kind: nodesdk.PortAITool, MaxConnections: -1}}
		},
		"max on main input":    func(d *nodesdk.Descriptor) { d.Inputs = []nodesdk.PortSpec{{MaxConnections: 1}} },
		"max on output":        func(d *nodesdk.Descriptor) { d.Outputs[0].MaxConnections = 1 },
		"sub-node main input":  func(d *nodesdk.Descriptor) { d.Inputs = []nodesdk.PortSpec{{}} },
		"sub-node main output": func(d *nodesdk.Descriptor) { d.Outputs = append(d.Outputs, nodesdk.PortSpec{}) },
	} {
		d := subNode
		d.Outputs = append([]nodesdk.PortSpec(nil), subNode.Outputs...)
		mutate(&d)
		if d.Validate() == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
}
