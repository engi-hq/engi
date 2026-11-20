package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// enabledSubs lists the enabled sub-nodes connected to one sub-port input, in
// connection order. Disabled sub-nodes are skipped.
func (r *runner) enabledSubs(info *nodeInfo, key subKey) []*nodeInfo {
	var out []*nodeInfo
	for _, name := range info.subs[key] {
		if sub := r.g.nodes[name]; !sub.node.Disabled {
			out = append(out, sub)
		}
	}
	return out
}

// checkRequiredSubPorts fails before the node runs when a Required sub-port
// input has no enabled sub-node. Like any pre-run check it is not retried.
func (r *runner) checkRequiredSubPorts(info *nodeInfo) error {
	ordinals := map[nodesdk.PortKind]int{}
	for _, spec := range info.desc.Inputs {
		kind := spec.EffectiveKind()
		ord := ordinals[kind]
		ordinals[kind]++
		if !kind.IsSub() || !spec.Required {
			continue
		}
		if len(r.enabledSubs(info, subKey{kind: kind, ordinal: ord})) == 0 {
			return fmt.Errorf("%s input is required: connect and enable a node on it", kind)
		}
	}
	return nil
}

// SubPort implements nodesdk.Input: it pulls the values of the sub-nodes
// connected to this node's sub-port inputs of the given kind.
func (n *nodeInput) SubPort(ctx context.Context, kind nodesdk.PortKind, itemIndex int) ([]nodesdk.Supplied, error) {
	itemIndex = n.itemFor(itemIndex)
	if !kind.IsSub() || n.info.desc.PortCount(true, kind) == 0 {
		return nil, fmt.Errorf("node %q has no %q sub-port input", n.info.node.Name, kind)
	}
	var out []nodesdk.Supplied
	for ord := range n.info.desc.PortCount(true, kind) {
		spec, _ := n.info.desc.PortOfKind(true, kind, ord)
		subs := n.r.enabledSubs(n.info, subKey{kind: kind, ordinal: ord})
		if spec.Required && len(subs) == 0 {
			return nil, fmt.Errorf("%s input is required: connect and enable a node on it", kind)
		}
		for _, sub := range subs {
			v, err := n.supply(ctx, sub, itemIndex)
			if err != nil {
				return nil, err
			}
			out = append(out, nodesdk.Supplied{Node: sub.node.Name, Value: v})
		}
	}
	return out, nil
}

// supply calls one sub-node's Supply with an Input of its own. Its parameters
// and expressions resolve against the parent's item itemIndex, whatever index
// the sub-node passes, and it sees the
// parent's main items. Nothing is recorded in the run data (docs/14).
func (n *nodeInput) supply(ctx context.Context, sub *nodeInfo, itemIndex int) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = n.subError(sub, fmt.Errorf("panicked: %v", p))
		}
	}()
	c := *n
	c.info = sub
	c.bound, c.boundItem = true, itemIndex
	c.log = n.r.e.d.Logger.With("node", sub.node.Name, "parent", n.info.node.Name, "runIndex", n.runIndex)
	if sub.node.Settings.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, sub.node.Settings.Timeout)
		defer cancel()
	}
	v, err = sub.supplier.Supply(ctx, &c)
	if err != nil {
		return nil, n.subError(sub, err)
	}
	return v, nil
}

// subError attributes a sub-node failure to the sub-node by name. The parent
// node fails with it, so the run error names both.
func (n *nodeInput) subError(sub *nodeInfo, err error) error {
	ne := &nodesdk.NodeError{Message: fmt.Sprintf("sub-node %q (%s) failed", sub.node.Name, sub.node.Type), Err: err}
	var inner *nodesdk.NodeError
	if errors.As(err, &inner) {
		ne.Description, ne.HTTPStatus = inner.Description, inner.HTTPStatus
	}
	var xe *domain.ExpressionError
	if errors.As(err, &xe) {
		ne.Description = xe.Stack
	}
	return ne
}
