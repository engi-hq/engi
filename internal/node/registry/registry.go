// Package registry holds the catalogue of available node types. It is built by
// the composition root with explicit Register calls; there is no init()-time
// self-registration.
package registry

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Factory builds a fresh node instance.
type Factory func() nodesdk.Node

type entry struct {
	factory    Factory
	descriptor nodesdk.Descriptor
}

type key struct {
	nodeType string
	version  int
}

// Registry maps (type, version) to node factories. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	entries map[key]entry
	latest  map[string]int
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{entries: map[key]entry{}, latest: map[string]int{}}
}

// Register adds a node type. The descriptor is read once from a probe instance
// and validated; registering the same type and version twice is an error.
func (r *Registry) Register(f Factory) error {
	if f == nil {
		return errors.New("registering node: nil factory")
	}
	probe := f()
	if probe == nil {
		return errors.New("registering node: factory returned nil")
	}
	d := probe.Describe()
	if err := d.Validate(); err != nil {
		return fmt.Errorf("registering node %q: invalid descriptor: %w", d.Type, err)
	}
	k := key{d.Type, d.Version}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.entries[k]; dup {
		return fmt.Errorf("registering node %q version %d: already registered", d.Type, d.Version)
	}
	r.entries[k] = entry{factory: f, descriptor: d}
	if d.Version > r.latest[d.Type] {
		r.latest[d.Type] = d.Version
	}
	return nil
}

// Lookup returns a new instance of the node. A version of 0 selects the latest
// registered version. Unknown types wrap domain.ErrNotFound.
func (r *Registry) Lookup(nodeType string, version int) (nodesdk.Node, error) {
	e, err := r.find(nodeType, version)
	if err != nil {
		return nil, err
	}
	return e.factory(), nil
}

// Descriptor returns the descriptor of a node type without instantiating it
// for execution. A version of 0 selects the latest.
func (r *Registry) Descriptor(nodeType string, version int) (nodesdk.Descriptor, error) {
	e, err := r.find(nodeType, version)
	if err != nil {
		return nodesdk.Descriptor{}, err
	}
	return e.descriptor, nil
}

// Descriptors lists every registered descriptor ordered by type, then version.
func (r *Registry) Descriptors() []nodesdk.Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]nodesdk.Descriptor, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.descriptor)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Version < out[j].Version
	})
	return out
}

func (r *Registry) find(nodeType string, version int) (entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if version == 0 {
		version = r.latest[nodeType]
	}
	e, ok := r.entries[key{nodeType, version}]
	if !ok {
		return entry{}, fmt.Errorf("node type %q version %d: %w", nodeType, version, domain.ErrNotFound)
	}
	return e, nil
}
