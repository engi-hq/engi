package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// UserStore persists users. Emails are stored lower-cased by the caller.
type UserStore interface {
	// Create inserts the user. A taken email, or a second owner, fails with an
	// error wrapping domain.ErrAlreadyExists.
	Create(ctx context.Context, u domain.User) error
	Get(ctx context.Context, id domain.UserID) (domain.User, error)
	// GetByEmail returns an error wrapping domain.ErrNotFound when unknown.
	GetByEmail(ctx context.Context, email string) (domain.User, error)
	// Update writes email, name, password hash, role and the disabled flag.
	Update(ctx context.Context, u domain.User) error
	// Delete removes the user with their sessions and API keys.
	Delete(ctx context.Context, id domain.UserID) error
	// List returns users newest first.
	List(ctx context.Context, p Page) (PageResult[domain.User], error)
	Count(ctx context.Context) (int, error)
}

// SessionStore persists sessions by token hash.
type SessionStore interface {
	Create(ctx context.Context, s domain.Session) error
	// Get returns an error wrapping domain.ErrNotFound when unknown.
	Get(ctx context.Context, tokenHash string) (domain.Session, error)
	// Touch records the time the session was last used.
	Touch(ctx context.Context, tokenHash string, seen time.Time) error
	Delete(ctx context.Context, tokenHash string) error
	DeleteByUser(ctx context.Context, id domain.UserID) error
	// DeleteExpired removes sessions past their absolute expiry or last used
	// before idleCutoff, and returns how many.
	DeleteExpired(ctx context.Context, now, idleCutoff time.Time) (int, error)
}

// APIKeyStore persists API keys.
type APIKeyStore interface {
	Create(ctx context.Context, k domain.APIKey) error
	// GetByPrefix returns an error wrapping domain.ErrNotFound when unknown.
	GetByPrefix(ctx context.Context, prefix string) (domain.APIKey, error)
	Get(ctx context.Context, id domain.APIKeyID) (domain.APIKey, error)
	// ListByUser returns the user's keys newest first, revoked ones included.
	ListByUser(ctx context.Context, id domain.UserID, p Page) (PageResult[domain.APIKey], error)
	// CountActive counts the user's keys that are neither revoked nor expired.
	CountActive(ctx context.Context, id domain.UserID, now time.Time) (int, error)
	// Revoke marks the key revoked; revoking twice keeps the first time.
	Revoke(ctx context.Context, id domain.APIKeyID, at time.Time) error
	Touch(ctx context.Context, id domain.APIKeyID, at time.Time) error
}

// PasswordHasher hashes and verifies passwords.
type PasswordHasher interface {
	// Hash returns a self-describing encoded hash.
	Hash(password string) (string, error)
	// Verify reports whether the password matches the encoded hash.
	Verify(password, encoded string) (bool, error)
	// NeedsRehash reports whether the hash was made with weaker parameters
	// than the current ones.
	NeedsRehash(encoded string) bool
}
