package engine

import (
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// cloneValue deep-copies JSON-like data so nodes cannot alias each other's items.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// toSDKItems converts domain items for a node, deep-copying their JSON.
func toSDKItems(items []domain.Item) []nodesdk.Item {
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		s := nodesdk.Item{JSON: cloneMap(it.JSON)}
		if len(it.Binary) > 0 {
			s.Binary = make(map[string]nodesdk.BinaryRef, len(it.Binary))
			for k, b := range it.Binary {
				s.Binary[k] = nodesdk.BinaryRef(b)
			}
		}
		for _, p := range it.PairedItem {
			s.PairedItem = append(s.PairedItem, nodesdk.PairedItem(p))
		}
		if it.Error != nil {
			s.Error = &nodesdk.ItemError{Node: it.Error.Node, Message: it.Error.Message, Description: it.Error.Description, HTTPStatus: it.Error.HTTPStatus}
		}
		out[i] = s
	}
	return out
}

// fromSDKItems converts a node's output back into domain items.
func fromSDKItems(items []nodesdk.Item, node string) []domain.Item {
	out := make([]domain.Item, len(items))
	for i, it := range items {
		d := domain.Item{JSON: it.JSON}
		if len(it.Binary) > 0 {
			d.Binary = make(map[string]domain.BinaryRef, len(it.Binary))
			for k, b := range it.Binary {
				d.Binary[k] = domain.BinaryRef(b)
			}
		}
		for _, p := range it.PairedItem {
			d.PairedItem = append(d.PairedItem, domain.ItemRef(p))
		}
		if it.Error != nil {
			origin := it.Error.Node
			if origin == "" {
				origin = node
			}
			d.Error = &domain.ErrorInfo{
				Kind: domain.ErrorKindNode, Node: origin, Message: it.Error.Message,
				Description: it.Error.Description, HTTPStatus: it.Error.HTTPStatus,
			}
			d.JSON = map[string]any{errorKey: it.Error.Message}
		}
		out[i] = d
	}
	return out
}

// stampLineage returns copies of the items of one input port whose pairedItem
// points at their own position on that port. A node that hands an input item
// back as output then keeps a correct lineage.
func stampLineage(items []domain.Item, inputPort int) []domain.Item {
	out := make([]domain.Item, len(items))
	for i, it := range items {
		it.PairedItem = []domain.ItemRef{{Input: inputPort, Item: i}}
		out[i] = it
	}
	return out
}

// pairToAll references every item of every input.
func pairToAll(inputs [][]domain.Item) []domain.ItemRef {
	var refs []domain.ItemRef
	for p, items := range inputs {
		for i := range items {
			refs = append(refs, domain.ItemRef{Input: p, Item: i})
		}
	}
	return refs
}

// pinnedItems copies pinned items and pairs them like node output.
// Pinned items come from outside this run, so whatever pairing they carry
// refers to some other execution and is discarded.
func pinnedItems(pin []domain.Item, inputs [][]domain.Item) []domain.Item {
	out := append([]domain.Item(nil), pin...)
	for i := range out {
		out[i].PairedItem = nil
	}
	assignPairing(out, inputs, true)
	return out
}

// dropStalePairing removes pairedItem references that point at no input item of
// this run, for example references carried over from an earlier execution or
// computed against a different input. Valid references are kept.
func dropStalePairing(items []domain.Item, inputs [][]domain.Item) {
	for i := range items {
		refs := items[i].PairedItem
		if len(refs) == 0 {
			continue
		}
		kept := refs[:0:0]
		for _, ref := range refs {
			if ref.Input >= 0 && ref.Input < len(inputs) && ref.Item >= 0 && ref.Item < len(inputs[ref.Input]) {
				kept = append(kept, ref)
			}
		}
		items[i].PairedItem = kept
	}
}

// assignPairing fills in missing pairedItem where the mapping is unambiguous,
// with these rules: a single input item maps to every output
// item; with one output port, equal counts map one to one and a single output
// item of several inputs maps to the first. singlePort tells whether the node
// produced exactly one output port.
func assignPairing(out []domain.Item, inputs [][]domain.Item, singlePort bool) {
	if len(inputs) != 1 {
		return
	}
	in := inputs[0]
	for i := range out {
		if len(out[i].PairedItem) > 0 {
			continue
		}
		switch {
		case len(in) == 1:
			out[i].PairedItem = []domain.ItemRef{{Input: 0, Item: 0}}
		case singlePort && len(in) == len(out):
			out[i].PairedItem = []domain.ItemRef{{Input: 0, Item: i}}
		case singlePort && len(out) == 1 && len(in) > 1:
			out[i].PairedItem = []domain.ItemRef{{Input: 0, Item: 0}}
		}
	}
}
