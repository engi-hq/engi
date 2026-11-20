package domain

import (
	"fmt"
	"strings"
)

// Prefixes of the typed identifiers. An identifier is "<prefix>_<ULID>".
const (
	PrefixWorkflow   = "wf"
	PrefixExecution  = "ex"
	PrefixCredential = "cr"
	PrefixNode       = "nd"
	PrefixTag        = "tg"
	PrefixVariable   = "vr"

	// ulidLength is the length of the canonical ULID text encoding.
	ulidLength = 26
)

// WorkflowID identifies a workflow.
type WorkflowID string

// ExecutionID identifies an execution.
type ExecutionID string

// CredentialID identifies a credential.
type CredentialID string

// NodeID identifies a node within a workflow.
type NodeID string

// TagID identifies a tag.
type TagID string

// VariableID identifies a variable.
type VariableID string

// Validate reports whether the ID has the workflow prefix and a ULID body.
func (id WorkflowID) Validate() error { return validateID(PrefixWorkflow, string(id)) }

// Validate reports whether the ID has the execution prefix and a ULID body.
func (id ExecutionID) Validate() error { return validateID(PrefixExecution, string(id)) }

// Validate reports whether the ID has the credential prefix and a ULID body.
func (id CredentialID) Validate() error { return validateID(PrefixCredential, string(id)) }

// Validate reports whether the ID has the node prefix and a ULID body.
func (id NodeID) Validate() error { return validateID(PrefixNode, string(id)) }

// Validate reports whether the ID has the tag prefix and a ULID body.
func (id TagID) Validate() error { return validateID(PrefixTag, string(id)) }

// Validate reports whether the ID has the variable prefix and a ULID body.
func (id VariableID) Validate() error { return validateID(PrefixVariable, string(id)) }

func validateID(prefix, id string) error {
	body, ok := strings.CutPrefix(id, prefix+"_")
	if !ok {
		return fmt.Errorf("id %q must start with %q", id, prefix+"_")
	}
	if len(body) != ulidLength {
		return fmt.Errorf("id %q must have a %d character ULID after the prefix", id, ulidLength)
	}
	return nil
}
