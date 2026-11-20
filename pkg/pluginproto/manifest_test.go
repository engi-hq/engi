package pluginproto_test

import (
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

func TestParseManifest(t *testing.T) {
	tests := []struct {
		name, yaml, wantErr string
	}{
		{name: "stdio", yaml: "name: acme-text\nversion: 1.2.0\nruntime:\n  command: [python3, main.py]\nnodes: [acme.text.reverse]\n"},
		{name: "http", yaml: "name: acme\nversion: 0.1.0-rc.1\nruntime:\n  url: https://acme:8443\n  tokenFile: /t\nnodes: [acme.x]\n"},
		{name: "bad name", yaml: "name: Acme\nversion: 1.0.0\nruntime: {command: [x]}\nnodes: [a.b]\n", wantErr: "name"},
		{name: "bad version", yaml: "name: a\nversion: v1\nruntime: {command: [x]}\nnodes: [a.b]\n", wantErr: "semantic version"},
		{name: "both runtimes", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x], url: 'http://h'}\nnodes: [a.b]\n", wantErr: "exactly one"},
		{name: "no runtime", yaml: "name: a\nversion: 1.0.0\nnodes: [a.b]\n", wantErr: "exactly one"},
		{name: "relative url", yaml: "name: a\nversion: 1.0.0\nruntime: {url: /rpc}\nnodes: [a.b]\n", wantErr: "absolute"},
		{name: "env on url", yaml: "name: a\nversion: 1.0.0\nruntime: {url: 'http://h', env: {A: b}}\nnodes: [a.b]\n", wantErr: "env only"},
		{name: "reserved type", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x]}\nnodes: [engi.x]\n", wantErr: "reserved"},
		{name: "undotted type", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x]}\nnodes: [reverse]\n", wantErr: "dot-separated"},
		{name: "duplicate type", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x]}\nnodes: [a.b, a.b]\n", wantErr: "twice"},
		{name: "no nodes", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x]}\n", wantErr: "at least one"},
		{name: "unknown key", yaml: "name: a\nversion: 1.0.0\nruntime: {command: [x]}\nnodes: [a.b]\nnode: [a.c]\n", wantErr: "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pluginproto.ParseManifest([]byte(tt.yaml))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckDescriptor(t *testing.T) {
	main := []nodesdk.PortSpec{{Name: "main"}}
	ok := nodesdk.Descriptor{Type: "acme.x", Version: 1, DisplayName: "X", Group: nodesdk.GroupTransform, Inputs: main, Outputs: main}
	tests := []struct {
		name    string
		mutate  func(d *nodesdk.Descriptor)
		wantErr string
	}{
		{name: "ok", mutate: func(*nodesdk.Descriptor) {}},
		{name: "not declared", mutate: func(d *nodesdk.Descriptor) { d.Type = "acme.y" }, wantErr: "not listed"},
		{name: "trigger", mutate: func(d *nodesdk.Descriptor) { d.Group = nodesdk.GroupTrigger }, wantErr: "group"},
		{name: "sub-port", mutate: func(d *nodesdk.Descriptor) {
			d.Inputs = append(d.Inputs, nodesdk.PortSpec{Name: "m", Kind: nodesdk.PortAIModel})
		}, wantErr: "main ports"},
		{name: "invalid", mutate: func(d *nodesdk.Descriptor) { d.Version = 0 }, wantErr: "version must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ok
			tt.mutate(&d)
			err := pluginproto.CheckDescriptor(d, []string{"acme.x"})
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

func TestConversionsRoundTrip(t *testing.T) {
	d := nodesdk.Descriptor{
		Type: "acme.x", Version: 2, DisplayName: "X", Group: nodesdk.GroupAction, Icon: "box",
		Inputs: []nodesdk.PortSpec{{Name: "main"}}, Outputs: []nodesdk.PortSpec{{Name: "a"}, {Name: "b"}},
		Credentials: []nodesdk.CredentialRef{{Name: "api", Type: "acme.key", Required: true}},
		Properties: []nodesdk.Property{{Name: "mode", Type: nodesdk.PropOptions, Default: "a",
			Options:        []nodesdk.Option{{Name: "A", Value: "a"}},
			DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{"x": {true}}},
			Children:       []nodesdk.Property{{Name: "c", Type: nodesdk.PropString}}}},
	}
	back := pluginproto.FromDescriptor(d).ToDescriptor()
	if back.Type != d.Type || len(back.Outputs) != 2 || back.Properties[0].Options[0].Value != "a" ||
		back.Properties[0].Children[0].Name != "c" || back.Credentials[0].Type != "acme.key" ||
		back.Properties[0].DisplayOptions.Show["x"][0] != true {
		t.Fatalf("descriptor round trip lost data: %+v", back)
	}

	until := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	out := nodesdk.Output{
		Ports: [][]nodesdk.Item{{{JSON: map[string]any{"a": 1.0}, Binary: map[string]nodesdk.BinaryRef{"f": {Ref: "r", Size: 3}},
			PairedItem: []nodesdk.PairedItem{{Input: 0, Item: 2}}, Error: &nodesdk.ItemError{Message: "m"}}}, {}},
		Wait: &nodesdk.Wait{Until: until},
	}
	got := pluginproto.FromOutput(out).ToOutput()
	it := got.Ports[0][0]
	if len(got.Ports) != 2 || it.JSON["a"] != 1.0 || it.Binary["f"].Ref != "r" || it.PairedItem[0].Item != 2 ||
		it.Error.Message != "m" || !got.Wait.Until.Equal(until) {
		t.Fatalf("output round trip lost data: %+v", got)
	}
	if empty := pluginproto.ToItems([]pluginproto.Item{{}}); empty[0].JSON == nil {
		t.Fatal("nil json should become an empty object")
	}
}
