package nodesdk

import (
	"errors"
	"fmt"
	"strings"
)

// Group classifies a node in the palette.
type Group string

// Node groups.
const (
	GroupTrigger   Group = "trigger"
	GroupAction    Group = "action"
	GroupTransform Group = "transform"
	GroupFlow      Group = "flow"
	GroupAI        Group = "ai"
)

// PortKind is the kind of a port. "main" carries items between steps. Kinds
// that start with "ai." are sub-ports: the parent node pulls a value from the
// connected sub-node (see Supplier and Input.SubPort) instead of receiving
// items, and sub-nodes are never scheduled as steps.
type PortKind string

// Port kinds. New sub-port kinds only need a constant here (any "ai." prefixed
// name is valid); the engine treats them all alike.
const (
	// PortMain is the data-flow port kind.
	PortMain PortKind = "main"
	// PortAIModel supplies a chat model (port.ModelProvider).
	PortAIModel PortKind = "ai.model"
	// PortAITool supplies tools (port.ToolProvider).
	PortAITool PortKind = "ai.tool"
	// PortAIMemory supplies chat memory.
	PortAIMemory PortKind = "ai.memory"
	// PortAIEmbedding supplies an embedding model.
	PortAIEmbedding PortKind = "ai.embedding"
	// PortAIVectorStore supplies a vector store.
	PortAIVectorStore PortKind = "ai.vectorStore"
	// PortAITextSplitter supplies a text splitter.
	PortAITextSplitter PortKind = "ai.textSplitter"
)

// subPortPrefix starts every sub-port kind.
const subPortPrefix = "ai."

// IsSub reports whether k is a sub-port kind ("ai." followed by a name).
func (k PortKind) IsSub() bool {
	return len(k) > len(subPortPrefix) && strings.HasPrefix(string(k), subPortPrefix)
}

// Valid reports whether k is "main" or a sub-port kind. The empty kind is
// treated as main by PortSpec and is not valid here.
func (k PortKind) Valid() bool { return k == PortMain || k.IsSub() }

// PortSpec describes one input or output port.
type PortSpec struct {
	// Name is a short label, for example "true" or "false".
	Name string
	// Kind defaults to PortMain when empty.
	Kind        PortKind
	DisplayName string
	// Required marks an input that must be connected. For a sub-port input the
	// engine fails the parent before it runs when no enabled sub-node is
	// connected.
	Required bool
	// MaxConnections caps how many sub-nodes may connect to a sub-port input;
	// 0 means unlimited. Only valid on sub-port inputs.
	MaxConnections int
}

// EffectiveKind returns the port's kind, defaulting to PortMain.
func (p PortSpec) EffectiveKind() PortKind {
	if p.Kind == "" {
		return PortMain
	}
	return p.Kind
}

// MergePolicy tells the engine when a node with several inputs may run.
type MergePolicy string

// Merge policies. The zero value behaves like MergeWaitAll.
const (
	// MergeWaitAll runs once every connected input has data.
	MergeWaitAll MergePolicy = "waitAll"
	// MergeAny runs each time any input receives data.
	MergeAny MergePolicy = "any"
	// MergePartialOnFlush runs with whatever arrived once nothing else is
	// ready (and immediately when all connected inputs have data).
	MergePartialOnFlush MergePolicy = "partialOnFlush"
)

// Valid reports whether p is a known policy (empty counts as waitAll).
func (p MergePolicy) Valid() bool {
	switch p {
	case "", MergeWaitAll, MergeAny, MergePartialOnFlush:
		return true
	}
	return false
}

// CredentialRef declares a credential a node can use.
type CredentialRef struct {
	// Name is the key in the node's credentials map.
	Name string
	// Type is the credential type, for example "engi.httpBasicAuth".
	Type     string
	Required bool
}

// Descriptor is static node metadata. It drives validation, the UI form and docs.
type Descriptor struct {
	// Type is the namespaced identifier, for example "engi.http.request".
	Type        string
	Version     int
	DisplayName string
	Description string
	Group       Group
	// Icon is a key into Engi's own icon set.
	Icon        string
	Inputs      []PortSpec
	Outputs     []PortSpec
	Properties  []Property
	Credentials []CredentialRef
	MergePolicy MergePolicy
}

// Validate checks the descriptor for internal consistency.
func (d Descriptor) Validate() error {
	var errs []error
	if d.Type == "" {
		errs = append(errs, errors.New("type must not be empty"))
	}
	if d.Version < 1 {
		errs = append(errs, errors.New("version must be at least 1"))
	}
	if !d.MergePolicy.Valid() {
		errs = append(errs, fmt.Errorf("unknown merge policy %q", d.MergePolicy))
	}
	errs = append(errs, d.validatePorts()...)
	seen := map[string]struct{}{}
	for i, p := range d.Properties {
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("properties[%d]: name must not be empty", i))
			continue
		}
		if _, dup := seen[p.Name]; dup {
			errs = append(errs, fmt.Errorf("properties[%d]: duplicate name %q", i, p.Name))
		}
		seen[p.Name] = struct{}{}
		if !p.Type.Valid() {
			errs = append(errs, fmt.Errorf("properties[%d] %q: unknown type %q", i, p.Name, p.Type))
		}
	}
	return errors.Join(errs...)
}

// OutputKind returns the kind of output port i, defaulting to PortMain.
func (d Descriptor) OutputKind(i int) PortKind {
	if i >= 0 && i < len(d.Outputs) && d.Outputs[i].Kind != "" {
		return d.Outputs[i].Kind
	}
	return PortMain
}

// validatePorts checks port kinds and the sub-node shape: a node with a
// sub-port output is a sub-node and has no main ports.
func (d Descriptor) validatePorts() []error {
	var errs []error
	check := func(dir string, ports []PortSpec) {
		for i, p := range ports {
			k := p.EffectiveKind()
			if !k.Valid() {
				errs = append(errs, fmt.Errorf("%s[%d]: unknown port kind %q", dir, i, p.Kind))
			}
			if p.MaxConnections < 0 {
				errs = append(errs, fmt.Errorf("%s[%d]: maxConnections must not be negative", dir, i))
			}
			if p.MaxConnections > 0 && (dir != "inputs" || !k.IsSub()) {
				errs = append(errs, fmt.Errorf("%s[%d]: maxConnections only applies to sub-port inputs", dir, i))
			}
		}
	}
	check("inputs", d.Inputs)
	check("outputs", d.Outputs)
	if d.IsSubNode() && (d.PortCount(false, PortMain) > 0 || d.PortCount(true, PortMain) > 0) {
		errs = append(errs, errors.New("a sub-node (one with a sub-port output) must not have main ports"))
	}
	return errs
}

// PortCount returns how many inputs (or outputs) have the given kind.
func (d Descriptor) PortCount(inputs bool, kind PortKind) int {
	n := 0
	for _, p := range d.ports(inputs) {
		if p.EffectiveKind() == kind {
			n++
		}
	}
	return n
}

// PortOfKind returns the ordinal-th input (or output) of the given kind. A
// connection endpoint's Index is this ordinal, so for "main" it is the item
// port index the node sees.
func (d Descriptor) PortOfKind(inputs bool, kind PortKind, ordinal int) (PortSpec, bool) {
	if ordinal < 0 {
		return PortSpec{}, false
	}
	for _, p := range d.ports(inputs) {
		if p.EffectiveKind() != kind {
			continue
		}
		if ordinal == 0 {
			return p, true
		}
		ordinal--
	}
	return PortSpec{}, false
}

func (d Descriptor) ports(inputs bool) []PortSpec {
	if inputs {
		return d.Inputs
	}
	return d.Outputs
}

// IsSubNode reports whether the node supplies a value to a parent through a
// sub-port output. Sub-nodes implement Supplier and never run as steps.
func (d Descriptor) IsSubNode() bool {
	for _, p := range d.Outputs {
		if p.EffectiveKind().IsSub() {
			return true
		}
	}
	return false
}
