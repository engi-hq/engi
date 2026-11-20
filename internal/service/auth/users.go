package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

func requireOwner(actor Principal) error {
	if !actor.IsOwner() {
		return fmt.Errorf("only the owner may do this: %w", domain.ErrForbidden)
	}
	return nil
}

// CreateMember adds a member account with an initial password set by the
// owner (there is no email delivery to invite by link yet).
func (s *Service) CreateMember(ctx context.Context, actor Principal, in NewUser) (domain.User, error) {
	if err := requireOwner(actor); err != nil {
		return domain.User{}, err
	}
	u, err := s.newUser(in, domain.RoleMember)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.d.Users.Create(ctx, u); err != nil {
		return domain.User{}, fmt.Errorf("creating member: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = s.d.Clock.Now(), s.d.Clock.Now()
	s.d.Logger.InfoContext(ctx, "member created", "user", u.ID, "by", actor.User.ID)
	return u, nil
}

// ListUsers returns users newest first. Only the owner may list.
func (s *Service) ListUsers(ctx context.Context, actor Principal, p port.Page) (port.PageResult[domain.User], error) {
	if err := requireOwner(actor); err != nil {
		return port.PageResult[domain.User]{}, err
	}
	res, err := s.d.Users.List(ctx, p)
	if err != nil {
		return port.PageResult[domain.User]{}, fmt.Errorf("listing users: %w", err)
	}
	return res, nil
}

// GetUser returns a user. A member may read only themselves.
func (s *Service) GetUser(ctx context.Context, actor Principal, id domain.UserID) (domain.User, error) {
	if !actor.IsOwner() && actor.User.ID != id {
		return domain.User{}, fmt.Errorf("members may only read their own account: %w", domain.ErrForbidden)
	}
	u, err := s.d.Users.Get(ctx, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("getting user %s: %w", id, err)
	}
	return u, nil
}

// UserPatch changes a user; nil fields stay as they are.
type UserPatch struct {
	Email    *string
	Name     *string
	Disabled *bool
	// Password sets a new password. Changing one's own password needs
	// CurrentPassword.
	Password        *string
	CurrentPassword string
}

// UpdateUser applies a patch. Members may change their own email, name and
// password; the owner may also disable or enable other users. Changing the
// password or disabling a user ends all their sessions.
func (s *Service) UpdateUser(ctx context.Context, actor Principal, id domain.UserID, patch UserPatch) (domain.User, error) {
	self := actor.User.ID == id
	if !actor.IsOwner() && !self {
		return domain.User{}, fmt.Errorf("members may only change their own account: %w", domain.ErrForbidden)
	}
	u, err := s.d.Users.Get(ctx, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("getting user %s: %w", id, err)
	}
	endSessions := false
	if patch.Email != nil {
		if u.Email, err = normalizeEmail(*patch.Email); err != nil {
			return domain.User{}, err
		}
	}
	if patch.Name != nil {
		if u.Name = strings.TrimSpace(*patch.Name); u.Name == "" {
			return domain.User{}, invalid("name", "must not be empty")
		}
	}
	if patch.Disabled != nil {
		if !actor.IsOwner() || self {
			return domain.User{}, fmt.Errorf("only the owner may disable other users: %w", domain.ErrForbidden)
		}
		u.Disabled = *patch.Disabled
		endSessions = endSessions || u.Disabled
	}
	if patch.Password != nil {
		if err := s.checkPassword(*patch.Password); err != nil {
			return domain.User{}, err
		}
		if self {
			if ok, verr := s.d.Hasher.Verify(patch.CurrentPassword, u.PasswordHash); verr != nil || !ok {
				return domain.User{}, invalid("currentPassword", "is incorrect")
			}
		}
		if u.PasswordHash, err = s.d.Hasher.Hash(*patch.Password); err != nil {
			return domain.User{}, fmt.Errorf("hashing password: %w", err)
		}
		endSessions = true
	}
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.d.Users.Update(ctx, u); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		if endSessions {
			return s.d.Sessions.DeleteByUser(ctx, id) //nolint:wrapcheck // wrapped below
		}
		return nil
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("updating user %s: %w", id, err)
	}
	return u, nil
}

// DeleteUser removes a user with their sessions and API keys. The owner cannot
// be deleted.
func (s *Service) DeleteUser(ctx context.Context, actor Principal, id domain.UserID) error {
	if err := requireOwner(actor); err != nil {
		return err
	}
	if actor.User.ID == id {
		return invalid("id", "the owner account cannot be deleted")
	}
	if err := s.d.Users.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting user %s: %w", id, err)
	}
	s.d.Logger.InfoContext(ctx, "user deleted", "user", id, "by", actor.User.ID)
	return nil
}

// NewAPIKey describes a key to create.
type NewAPIKey struct {
	Name      string
	Scopes    []domain.Scope
	ExpiresAt *time.Time
}

// CreatedAPIKey is a new key together with its token, which is never
// retrievable again.
type CreatedAPIKey struct {
	Key   domain.APIKey
	Token string
}

// CreateAPIKey issues a key for the actor. API-key principals cannot mint new
// keys (a leaked key must not be able to spawn more).
func (s *Service) CreateAPIKey(ctx context.Context, actor Principal, in NewAPIKey) (CreatedAPIKey, error) {
	if actor.Kind != KindSession {
		return CreatedAPIKey{}, fmt.Errorf("api keys can only be created from a session: %w", domain.ErrForbidden)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return CreatedAPIKey{}, invalid("name", "must not be empty")
	}
	scopes := in.Scopes
	if len(scopes) == 0 {
		scopes = []domain.Scope{domain.ScopeRead}
	}
	for _, sc := range scopes {
		if !sc.Valid() {
			return CreatedAPIKey{}, invalid("scopes", fmt.Sprintf("unknown scope %q", sc))
		}
	}
	now := s.d.Clock.Now()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return CreatedAPIKey{}, invalid("expiresAt", "must be in the future")
	}
	n, err := s.d.APIKeys.CountActive(ctx, actor.User.ID, now)
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("counting api keys: %w", err)
	}
	if n >= s.cfg.APIKey.MaxPerUser {
		return CreatedAPIKey{}, invalid("name", fmt.Sprintf("limit of %d active api keys reached", s.cfg.APIKey.MaxPerUser))
	}
	lookup, err := s.randomHex(s.cfg.APIKey.LookupBytes)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	secret, err := s.randomToken(s.cfg.APIKey.SecretBytes)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	id, err := s.d.IDs.APIKeyID()
	if err != nil {
		return CreatedAPIKey{}, fmt.Errorf("creating api key id: %w", err)
	}
	prefix := s.cfg.APIKey.Prefix + "_" + lookup
	token := prefix + "_" + secret
	key := domain.APIKey{
		ID: id, UserID: actor.User.ID, Name: name, Prefix: prefix, SecretHash: hashToken(token),
		Scopes: scopes, ExpiresAt: in.ExpiresAt, CreatedAt: now,
	}
	if err := s.d.APIKeys.Create(ctx, key); err != nil {
		return CreatedAPIKey{}, fmt.Errorf("storing api key: %w", err)
	}
	s.d.Logger.InfoContext(ctx, "api key created", "key", id, "user", actor.User.ID)
	return CreatedAPIKey{Key: key, Token: token}, nil
}

func (s *Service) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.d.Random, b); err != nil {
		return "", fmt.Errorf("generating key id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

// ListAPIKeys returns the actor's keys (revoked ones included) newest first.
func (s *Service) ListAPIKeys(ctx context.Context, actor Principal, p port.Page) (port.PageResult[domain.APIKey], error) {
	res, err := s.d.APIKeys.ListByUser(ctx, actor.User.ID, p)
	if err != nil {
		return port.PageResult[domain.APIKey]{}, fmt.Errorf("listing api keys: %w", err)
	}
	return res, nil
}

// RevokeAPIKey revokes one of the actor's keys; the owner may revoke any key.
// An unknown key, and another user's key for a member, both read as not found.
func (s *Service) RevokeAPIKey(ctx context.Context, actor Principal, id domain.APIKeyID) error {
	key, err := s.d.APIKeys.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("revoking api key %s: %w", id, err)
	}
	if key.UserID != actor.User.ID && !actor.IsOwner() {
		return fmt.Errorf("revoking api key %s: %w", id, domain.ErrNotFound)
	}
	if err := s.d.APIKeys.Revoke(ctx, id, s.d.Clock.Now()); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("revoking api key %s: %w", id, err)
	}
	s.d.Logger.InfoContext(ctx, "api key revoked", "key", id, "by", actor.User.ID)
	return nil
}
