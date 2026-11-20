package engine

import (
	"fmt"
	"sort"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type edge struct {
	to      string
	toIndex int
}

type nodeInfo struct {
	node *domain.Node
	desc nodesdk.Descriptor
	// out maps an output port index to its main connections.
	out map[int][]edge
	// connected is the set of input indexes with at least one incoming connection.
	connected map[int]bool
	// reverse lists source node names (for ancestor search).
	sources []string
	// supplier is set when the node implements nodesdk.Supplier.
	supplier nodesdk.Supplier
	// subs lists, per sub-port input, the names of the connected sub-nodes in
	// connection order.
	subs map[subKey][]string
}

// subKey addresses one sub-port input: its kind and its ordinal among the
// inputs of that kind (the connection's To.Index).
type subKey struct {
	kind    nodesdk.PortKind
	ordinal int
}

// isSub reports whether the node is a sub-node (never scheduled as a step).
func (n *nodeInfo) isSub() bool { return n.desc.IsSubNode() }

// inputs is the number of input slots a task for this node carries.
func (n *nodeInfo) inputs() int { return max(n.desc.PortCount(true, nodesdk.PortMain), 1) }

// multiInput reports whether the node has several declared inputs.
func (n *nodeInfo) multiInput() bool { return n.desc.PortCount(true, nodesdk.PortMain) > 1 }

// errorPort is the output index of the dedicated error port.
func (n *nodeInfo) errorPort() int { return n.desc.PortCount(false, nodesdk.PortMain) }

// graph is the validated, indexed form of a workflow.
type graph struct {
	nodes map[string]*nodeInfo
	// rank is each node's position in canvas order.
	rank map[string]int
	// canvas lists node names in canvas order.
	canvas []string
}

func buildGraph(wf *domain.Workflow, reg NodeRegistry) (*graph, error) {
	if err := wf.Validate(); err != nil {
		return nil, &domain.WorkflowError{Message: "invalid workflow", Err: err}
	}
	g := &graph{nodes: make(map[string]*nodeInfo, len(wf.Nodes)), rank: map[string]int{}}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		inst, err := reg.Lookup(n.Type, n.TypeVersion)
		if err != nil {
			return nil, &domain.WorkflowError{Node: n.Name, Message: "unknown node type", Err: err}
		}
		info := &nodeInfo{node: n, desc: inst.Describe(), out: map[int][]edge{}, connected: map[int]bool{}, subs: map[subKey][]string{}}
		info.supplier, _ = inst.(nodesdk.Supplier)
		g.nodes[n.Name] = info
	}
	g.canvas = wf.NodesInCanvasOrder()
	for i, name := range g.canvas {
		g.rank[name] = i
	}
	for _, c := range wf.Connections {
		from, to := g.nodes[c.From.Node], g.nodes[c.To.Node]
		if kind := nodesdk.PortKind(c.From.Port); kind != nodesdk.PortMain {
			if err := g.connectSub(kind, c, from, to); err != nil {
				return nil, err
			}
			continue // sub-ports are pulled by their parent node, not scheduled
		}
		maxOut := from.errorPort()
		if from.node.Settings.OnError == domain.OnErrorErrorOutput {
			maxOut++
		}
		if c.From.Index >= maxOut {
			return nil, &domain.WorkflowError{Node: from.node.Name,
				Message: fmt.Sprintf("connection uses output %d but the node has %d", c.From.Index, maxOut)}
		}
		if mainIn := to.desc.PortCount(true, nodesdk.PortMain); c.To.Index >= mainIn {
			return nil, &domain.WorkflowError{Node: to.node.Name,
				Message: fmt.Sprintf("connection uses input %d but the node has %d", c.To.Index, mainIn)}
		}
		from.out[c.From.Index] = append(from.out[c.From.Index], edge{to: to.node.Name, toIndex: c.To.Index})
		to.connected[c.To.Index] = true
		to.sources = append(to.sources, from.node.Name)
	}
	if err := g.checkSubPorts(); err != nil {
		return nil, err
	}
	return g, nil
}

// connectSub records a sub-port connection from a sub-node output to a parent
// sub-port input and checks that both ends exist, match in kind and that the
// sub-node can supply.
func (g *graph) connectSub(kind nodesdk.PortKind, c domain.Connection, from, to *nodeInfo) error {
	if !kind.IsSub() {
		return &domain.WorkflowError{Node: from.node.Name, Message: fmt.Sprintf("unknown port kind %q", kind)}
	}
	if _, ok := from.desc.PortOfKind(false, kind, c.From.Index); !ok {
		return &domain.WorkflowError{Node: from.node.Name,
			Message: fmt.Sprintf("connection uses %s output %d but the node has no such output", kind, c.From.Index)}
	}
	if from.supplier == nil {
		return &domain.WorkflowError{Node: from.node.Name,
			Message: fmt.Sprintf("node has a %s output but does not implement Supply", kind)}
	}
	if _, ok := to.desc.PortOfKind(true, kind, c.To.Index); !ok {
		return &domain.WorkflowError{Node: to.node.Name,
			Message: fmt.Sprintf("%s does not accept %s connections (input %d)", to.node.Name, kind, c.To.Index)}
	}
	key := subKey{kind: kind, ordinal: c.To.Index}
	to.subs[key] = append(to.subs[key], from.node.Name)
	return nil
}

// checkSubPorts enforces MaxConnections and rejects supply cycles. A missing
// Required connection is not a graph error (the editor saves unfinished
// workflows); the engine reports it when the parent is about to run.
func (g *graph) checkSubPorts() error {
	for _, name := range g.canvas {
		info := g.nodes[name]
		for key, names := range info.subs {
			spec, _ := info.desc.PortOfKind(true, key.kind, key.ordinal)
			if spec.MaxConnections > 0 && len(names) > spec.MaxConnections {
				return &domain.WorkflowError{Node: name, Message: fmt.Sprintf(
					"%s input accepts at most %d connection(s) but has %d", key.kind, spec.MaxConnections, len(names))}
			}
		}
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(name string) error
	visit = func(name string) error {
		state[name] = visiting
		for _, names := range g.nodes[name].subs {
			for _, sub := range names {
				switch state[sub] {
				case visiting:
					return &domain.WorkflowError{Node: sub, Message: "sub-port connections form a cycle"}
				case 0:
					if err := visit(sub); err != nil {
						return err
					}
				}
			}
		}
		state[name] = done
		return nil
	}
	for _, name := range g.canvas {
		if state[name] == 0 {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// ancestors returns the set containing target and every node that can reach it.
func (g *graph) ancestors(target string) map[string]bool {
	seen := map[string]bool{target: true}
	stack := []string{target}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, s := range g.nodes[cur].sources {
			if !seen[s] {
				seen[s] = true
				stack = append(stack, s)
			}
		}
	}
	return seen
}

// reaches reports whether to is reachable from from along main connections.
func (g *graph) reaches(from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == to {
			return true
		}
		for _, edges := range g.nodes[cur].out {
			for _, e := range edges {
				if !seen[e.to] {
					seen[e.to] = true
					stack = append(stack, e.to)
				}
			}
		}
	}
	return false
}

// delivery is data travelling along one connection.
type delivery struct {
	edge
	fromPort int
	items    []domain.Item
}

// deliveries lists the non-empty outputs' deliveries in canvas order of the
// target, then output port, then input index.
func (g *graph) deliveries(n *nodeInfo, outputs [][]domain.Item) []delivery {
	var ds []delivery
	for port, items := range outputs {
		if len(items) == 0 {
			continue
		}
		for _, e := range n.out[port] {
			ds = append(ds, delivery{edge: e, fromPort: port, items: items})
		}
	}
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if g.rank[a.to] != g.rank[b.to] {
			return g.rank[a.to] < g.rank[b.to]
		}
		if a.fromPort != b.fromPort {
			return a.fromPort < b.fromPort
		}
		return a.toIndex < b.toIndex
	})
	return ds
}
