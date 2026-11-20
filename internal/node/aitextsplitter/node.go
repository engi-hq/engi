// Package aitextsplitter implements the Text Splitter sub-node
// (engi.ai.textSplitter). It supplies a port.TextSplitter, a recursive
// character splitter, to the ai.textSplitter input of a vector store node.
package aitextsplitter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.ai.textSplitter"

// Defaults of the parameters (also the descriptor defaults).
const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 200
)

// defaultSeparators rank paragraph, line, word and character cuts.
var defaultSeparators = []any{"\n\n", "\n", " ", ""}

// Node is the Text Splitter sub-node.
type Node struct{}

var _ nodesdk.Supplier = (*Node)(nil)

// New returns a Text Splitter node.
func New() nodesdk.Node { return &Node{} }

// Describe implements nodesdk.Node.
func (n *Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Text Splitter", Group: nodesdk.GroupAI, Icon: "scissors",
		Description: "Cuts long text into overlapping chunks for embedding. Connect it to the Text Splitter input of a Vector Store node.",
		Outputs:     []nodesdk.PortSpec{{Name: "textSplitter", Kind: nodesdk.PortAITextSplitter}},
		Properties: []nodesdk.Property{
			{Name: "chunkSize", DisplayName: "Chunk size", Type: nodesdk.PropNumber, Default: float64(defaultChunkSize),
				Description: "Largest chunk, in characters (not bytes)."},
			{Name: "chunkOverlap", DisplayName: "Chunk overlap", Type: nodesdk.PropNumber, Default: float64(defaultChunkOverlap),
				Description: "Characters repeated at the start of the next chunk. Must be smaller than the chunk size."},
			{Name: "separators", DisplayName: "Separators", Type: nodesdk.PropJSON, Default: defaultSeparators,
				Description: "JSON array of separators, tried in order; the empty string cuts between characters. Default: paragraph, line, space, character."},
			{Name: "keepSeparator", DisplayName: "Keep separator", Type: nodesdk.PropBoolean, Default: true,
				Description: "Keep the separator at the start of the piece that followed it instead of dropping it."},
		},
	}
}

// Execute implements nodesdk.Node. Sub-nodes never run as steps.
func (n *Node) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, fmt.Errorf("text splitter: %w", nodesdk.ErrUnavailable)
}

// Supply implements nodesdk.Supplier and returns a port.TextSplitter.
func (n *Node) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	size, err := nodesdk.ParamInt(ctx, in, "chunkSize", 0)
	if err != nil {
		return nil, err
	}
	overlap, err := nodesdk.ParamInt(ctx, in, "chunkOverlap", 0)
	if err != nil {
		return nil, err
	}
	keep, err := nodesdk.ParamBool(ctx, in, "keepSeparator", 0)
	if err != nil {
		return nil, err
	}
	raw, err := in.Param(ctx, "separators", 0)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", "separators", err)
	}
	seps, err := separators(raw)
	if err != nil {
		return nil, err
	}
	s, err := NewRecursive(size, overlap, seps, keep)
	if err != nil {
		return nil, fmt.Errorf("text splitter: %w", err)
	}
	return s, nil
}

// separators reads the parameter: an array of strings, JSON text of one, or
// nothing (the defaults).
func separators(v any) ([]string, error) {
	if s, ok := v.(string); ok {
		if s == "" {
			v = nil
		} else {
			var decoded any
			if err := json.Unmarshal([]byte(s), &decoded); err != nil {
				return nil, fmt.Errorf("separators must be a JSON array of strings: %w", err)
			}
			v = decoded
		}
	}
	if v == nil {
		v = defaultSeparators
	}
	arr, ok := v.([]any)
	if !ok {
		if strs, isStrs := v.([]string); isStrs {
			return strs, nil
		}
		return nil, fmt.Errorf("separators must be a JSON array of strings, got %s", nodekit.TypeName(v))
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("separators[%d] must be a string, got %s", i, nodekit.TypeName(e))
		}
		out[i] = s
	}
	return out, nil
}
