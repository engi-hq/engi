package domain

import (
	"errors"
	"fmt"
	"time"
)

// Prefixes of the authentication identifiers.
const (
	PrefixUser   = "us"
	PrefixAPIKey = "ak"
)

// UserID identifies a user.
type UserID string

// APIKeyID identifies an API key.
type APIKeyID string

// Validate reports whether the ID has the user prefix and a ULID body.
func (id UserID) Validate() error { return validateID(PrefixUser, string(id)) }

// Validate reports whether the ID has the API key prefix and a ULID body.
func (id APIKeyID) Validate() error { return validateID(PrefixAPIKey, string(id)) }

// Authentication and authorization errors; check with errors.Is.
var (
	// ErrUnauthenticated: the caller did not prove who they are, or the
	// credentials were wrong, expired or revoked.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrForbidden: the caller is known but may not do this.
	ErrForbidden = errors.New("forbidden")
	// ErrRateLimited: too many attempts; see RateLimitedError.RetryAfter.
	ErrRateLimited = errors.New("rate limited")
	// ErrSetupDone: the first-run setup was already completed.
	ErrSetupDone = errors.New("setup already completed")
)

// RateLimitedError says when the caller may try again.
type RateLimitedError struct {
	RetryAfter time.Duration
}

// Error implements error.
func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("too many attempts, retry in %s", e.RetryAfter.Round(time.Second))
}

// Is makes errors.Is(err, ErrRateLimited) true.
func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// Role is a user's global role. Per-project roles arrive with Projects.
type Role string

// Roles. The owner is created by the first-run setup; members are invited by
// the owner.
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleMember }

// User is an account. PasswordHash is an encoded argon2id hash.
type User struct {
	ID           UserID
	Email        string
	Name         string
	PasswordHash string
	Role         Role
	Disabled     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session is a server-side browser session. The cookie holds the token; only
// its hash is stored.
type Session struct {
	TokenHash  string
	UserID     UserID
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time
}

// Scope limits what an API key may do.
type Scope string

// API key scopes: read allows safe requests; write allows everything read does
// plus changes.
const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { return s == ScopeRead || s == ScopeWrite }

// APIKey is a long-lived credential for automation. Prefix identifies the key
// (it is the non-secret part of the token); SecretHash is the SHA-256 of the
// whole token.
type APIKey struct {
	ID         APIKeyID
	UserID     UserID
	Name       string
	Prefix     string
	SecretHash string
	Scopes     []Scope
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// Active reports whether the key is neither revoked nor expired at now.
func (k APIKey) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	return k.ExpiresAt == nil || now.Before(*k.ExpiresAt)
}

// Permits reports whether the scopes allow the access: a write needs the write
// scope, a read is allowed by either scope.
func (k APIKey) Permits(write bool) bool {
	for _, s := range k.Scopes {
		if s == ScopeWrite || (!write && s == ScopeRead) {
			return true
		}
	}
	return false
}
