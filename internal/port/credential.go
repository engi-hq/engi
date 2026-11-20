package port

import (
	"context"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// CredentialData is a decrypted credential, valid for one node call only.
// It must never be logged or stored.
type CredentialData struct {
	Type string
	// ID and Version identify the stored credential and its last edit.
	ID      string
	Version int64
	Data    map[string]any
	// Auth is the credential type's authenticate declaration, nil if none.
	// Its field set must match nodesdk.Credential (the engine converts).
	Auth *nodesdk.AuthSpec
}

// CredentialResolver decrypts a credential right before node execution.
// Implementations return an error wrapping domain.ErrNotFound for unknown IDs.
type CredentialResolver interface {
	Resolve(ctx context.Context, id domain.CredentialID) (CredentialData, error)
}
