// Package registry holds the credential types known to a process. It is built
// explicitly by the composition root; there is no global registration.
package registry

import (
	"fmt"
	"sort"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Registry maps type names to credential types. Build it with New and Register
// before use; it is read-only afterwards and safe for concurrent reads.
type Registry struct {
	types map[string]nodesdk.CredentialType
}

// New returns an empty Registry.
func New() *Registry { return &Registry{types: map[string]nodesdk.CredentialType{}} }

// Register adds a type, failing on invalid or duplicate types.
func (r *Registry) Register(t nodesdk.CredentialType) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("credential type %q: %w", t.Name, err)
	}
	if _, dup := r.types[t.Name]; dup {
		return fmt.Errorf("credential type %q is already registered", t.Name)
	}
	r.types[t.Name] = t
	return nil
}

// Get returns the type with the given name.
func (r *Registry) Get(name string) (nodesdk.CredentialType, bool) {
	t, ok := r.types[name]
	return t, ok
}

// List returns all types ordered by name.
func (r *Registry) List() []nodesdk.CredentialType {
	out := make([]nodesdk.CredentialType, 0, len(r.types))
	for _, t := range r.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
