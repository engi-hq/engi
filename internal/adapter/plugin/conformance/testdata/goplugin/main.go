// Command goplugin is the Go plugin used by the conformance tests. It serves
// the reverse node by default, or the callback node when run as "goplugin eval".
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
	"github.com/engi-hq/engi/pkg/pluginsdk"
)

type reverse struct{}

func (reverse) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: "example.go.reverse", Version: 1, DisplayName: "Reverse Text", Group: nodesdk.GroupTransform,
		Inputs:  []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
		Outputs: []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
		Properties: []nodesdk.Property{
			{Name: "field", DisplayName: "Field", Type: nodesdk.PropString, Default: "text", Required: true},
		},
	}
}

func (reverse) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i, item := range in.Items(0) {
		field, err := nodesdk.ParamString(ctx, in, "field", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		s, ok := item.JSON[field].(string)
		if !ok {
			idx := i
			return nodesdk.Output{}, &nodesdk.NodeError{Message: fmt.Sprintf("field %q is not a string", field), ItemIndex: &idx}
		}
		r := []rune(s)
		for a, b := 0, len(r)-1; a < b; a, b = a+1, b-1 {
			r[a], r[b] = r[b], r[a]
		}
		out = append(out, nodesdk.Item{
			JSON:       map[string]any{"reversed": string(r)},
			PairedItem: []nodesdk.PairedItem{{Input: 0, Item: i}},
		})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

type eval struct{}

func (eval) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: "conformance.go.eval", Version: 1, DisplayName: "Eval", Group: nodesdk.GroupTransform,
		Inputs:  []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
		Outputs: []nodesdk.PortSpec{{Name: "main", Kind: nodesdk.PortMain}},
	}
}

func (eval) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	in.Logger().Info("evaluating", "step", "eval")
	var out []nodesdk.Item
	for i := range in.Items(0) {
		v, err := in.EvalExpression(ctx, "1+1", i)
		if err != nil {
			return nodesdk.Output{}, err
		}
		out = append(out, nodesdk.Item{JSON: map[string]any{"value": v}})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	node, name := nodesdk.Node(reverse{}), "conformance-go"
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		node, name = eval{}, "conformance-go-eval"
	}
	if err := pluginsdk.Serve(ctx, pluginproto.Info{Name: name, Version: "0.1.0"}, node); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
