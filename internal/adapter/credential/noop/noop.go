// Package noop provides a CredentialResolver that knows no credentials. It
// stands in until the credential store exists.
package noop

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Resolver always reports the credential as not found.
type Resolver struct{}

// Resolve implements port.CredentialResolver.
func (Resolver) Resolve(_ context.Context, id domain.CredentialID) (port.CredentialData, error) {
	return port.CredentialData{}, fmt.Errorf("credential %q: %w", id, domain.ErrNotFound)
}
