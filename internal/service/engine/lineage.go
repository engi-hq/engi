package engine

import (
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
)

// lineage answers $("Node") questions by walking pairedItem links backwards
// through the recorded runs.
type lineage struct {
	st        *State
	node      string
	runIndex  int
	sources   []domain.Source // sources of the node run being evaluated
	itemIndex int
}

// errAmbiguousPairing is returned when the current item descends from several
// different items of the requested node.
var errAmbiguousPairing = errors.New("ambiguous pairing")

// itemKey identifies one output item of one node run.
type itemKey struct {
	node     string
	runIndex int
	port     int
	item     int
}

// found is an item together with its identity, so that two lineage paths that
// reach the same item are recognised as agreeing.
type found struct {
	item domain.Item
	key  itemKey
}

// PairedItem implements port.NodeData. It follows every pairedItem reference of
// each hop: all paths must reach the same item of the target node.
func (l *lineage) PairedItem(target string) (domain.Item, error) {
	if len(l.st.RunData[target]) == 0 {
		return domain.Item{}, fmt.Errorf("node %q has not run: %w", target, domain.ErrNotFound)
	}
	if len(l.sources) == 0 || l.sources[0].Node == "" {
		return domain.Item{}, fmt.Errorf("node %q is not on the path of the current item: %w", target, domain.ErrNotFound)
	}
	res, err := l.resolve(target, l.sources[0], l.itemIndex, map[itemKey]resolved{})
	if err != nil {
		return domain.Item{}, err
	}
	return res.item, nil
}

type resolved struct {
	found
	err error
}

// resolve finds the item of target that output item idx of src descends from.
func (l *lineage) resolve(target string, src domain.Source, idx int, memo map[itemKey]resolved) (found, error) {
	key := itemKey{src.Node, src.RunIndex, src.OutputIndex, idx}
	if m, ok := memo[key]; ok {
		return m.found, m.err
	}
	f, err := l.resolveUncached(target, src, idx, key, memo)
	memo[key] = resolved{f, err}
	return f, err
}

func (l *lineage) resolveUncached(target string, src domain.Source, idx int, key itemKey, memo map[itemKey]resolved) (found, error) {
	out, ok := l.outputItems(src)
	if !ok || idx < 0 || idx >= len(out) {
		return found{}, fmt.Errorf("node %q has no item %d to pair with: %w", src.Node, idx, domain.ErrNotFound)
	}
	item := out[idx]
	if src.Node == target {
		return found{item, key}, nil
	}
	if len(item.PairedItem) == 0 {
		return found{}, fmt.Errorf("pairedItem information is missing on node %q, so the item of %q cannot be traced: %w", src.Node, target, domain.ErrNotFound)
	}
	srcs := l.st.RunData[src.Node][src.RunIndex].Sources
	var oks []found
	var firstErr error
	for _, ref := range item.PairedItem {
		if ref.Input < 0 || ref.Input >= len(srcs) || srcs[ref.Input].Node == "" {
			continue
		}
		f, err := l.resolve(target, srcs[ref.Input], ref.Item, memo)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		oks = append(oks, f)
	}
	switch {
	case len(oks) == 0 && firstErr != nil:
		return found{}, firstErr
	case len(oks) == 0:
		return found{}, fmt.Errorf("node %q is not on the path of the current item: %w", target, domain.ErrNotFound)
	}
	for _, f := range oks[1:] {
		if f.key != oks[0].key {
			return found{}, fmt.Errorf("multiple items of node %q match the current item (it combines several of them), use .first(), .last() or .all() instead: %w", target, errAmbiguousPairing)
		}
	}
	return oks[0], nil
}

// Output implements port.NodeData.
func (l *lineage) Output(target string) ([]domain.Item, error) {
	runs := l.st.RunData[target]
	if len(runs) == 0 {
		return nil, fmt.Errorf("node %q has not run: %w", target, domain.ErrNotFound)
	}
	ports := runs[len(runs)-1].Output[domain.PortMain]
	if len(ports) == 0 {
		return nil, nil
	}
	return ports[0], nil
}

func (l *lineage) outputItems(src domain.Source) ([]domain.Item, bool) {
	runs := l.st.RunData[src.Node]
	if src.RunIndex >= len(runs) {
		return nil, false
	}
	ports := runs[src.RunIndex].Output[domain.PortMain]
	if src.OutputIndex >= len(ports) {
		return nil, false
	}
	return ports[src.OutputIndex], true
}
