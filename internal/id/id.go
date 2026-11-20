// Package id generates prefixed ULID identifiers such as "wf_01J...".
package id

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/engi-hq/engi/internal/domain"
)

// Generator creates identifiers. It is safe for concurrent use and monotonic
// within one millisecond. Time and entropy are injected for deterministic tests.
type Generator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy io.Reader
}

// NewGenerator returns a Generator. entropy should be crypto/rand.Reader in
// production; it is wrapped to guarantee monotonic ordering.
func NewGenerator(now func() time.Time, entropy io.Reader) *Generator {
	return &Generator{now: now, entropy: ulid.Monotonic(entropy, 0)}
}

// New returns "<prefix>_<ULID>".
func (g *Generator) New(prefix string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u, err := ulid.New(ulid.Timestamp(g.now()), g.entropy)
	if err != nil {
		return "", fmt.Errorf("generating ulid: %w", err)
	}
	return prefix + "_" + u.String(), nil
}

// WorkflowID returns a new workflow ID.
func (g *Generator) WorkflowID() (domain.WorkflowID, error) {
	s, err := g.New(domain.PrefixWorkflow)
	return domain.WorkflowID(s), err
}

// ExecutionID returns a new execution ID.
func (g *Generator) ExecutionID() (domain.ExecutionID, error) {
	s, err := g.New(domain.PrefixExecution)
	return domain.ExecutionID(s), err
}

// CredentialID returns a new credential ID.
func (g *Generator) CredentialID() (domain.CredentialID, error) {
	s, err := g.New(domain.PrefixCredential)
	return domain.CredentialID(s), err
}

// NodeID returns a new node ID.
func (g *Generator) NodeID() (domain.NodeID, error) {
	s, err := g.New(domain.PrefixNode)
	return domain.NodeID(s), err
}

// JobID returns a new queue job ID.
func (g *Generator) JobID() (domain.JobID, error) {
	s, err := g.New(domain.PrefixJob)
	return domain.JobID(s), err
}

// TagID returns a new tag ID.
func (g *Generator) TagID() (domain.TagID, error) {
	s, err := g.New(domain.PrefixTag)
	return domain.TagID(s), err
}

// VariableID returns a new variable ID.
func (g *Generator) VariableID() (domain.VariableID, error) {
	s, err := g.New(domain.PrefixVariable)
	return domain.VariableID(s), err
}
