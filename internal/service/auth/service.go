// Package auth holds the use cases around identity: first-run owner setup,
// login and sessions, user management and API keys. Passwords are hashed with
// the injected port.PasswordHasher; session tokens and API keys are random,
// shown once and stored only as SHA-256 hashes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// IDGenerator creates identifiers.
type IDGenerator interface {
	UserID() (domain.UserID, error)
	APIKeyID() (domain.APIKeyID, error)
}

// Deps are the collaborators of Service. All but Random and Logger are required.
type Deps struct {
	Users    port.UserStore
	Sessions port.SessionStore
	APIKeys  port.APIKeyStore
	Hasher   port.PasswordHasher
	Tx       port.TxManager
	Clock    port.Clock
	IDs      IDGenerator
	// Random is the entropy source for tokens; defaults to crypto/rand.
	Random io.Reader
	Logger *slog.Logger
}

// Service implements the authentication use cases. It is safe for concurrent use.
type Service struct {
	cfg     config.AuthConfig
	d       Deps
	limiter *loginLimiter
	// decoy is verified when a login names an unknown account, so response
	// time does not reveal whether the account exists. It is created on first
	// use: hashing at startup would allocate the full argon2 memory (and keep
	// it in the process's RSS) for a feature most deployments rarely hit.
	decoy decoyHash
}

// decoyHash lazily computes one password hash.
type decoyHash struct {
	once sync.Once
	hash string
}

// get returns the hash, computing it on first use. A hashing failure yields an
// empty string, which Verify rejects as unusable.
func (d *decoyHash) get(h port.PasswordHasher) string {
	d.once.Do(func() { d.hash, _ = h.Hash("decoy") })
	return d.hash
}

// New builds a Service.
func New(cfg config.AuthConfig, d Deps) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid auth config: %w", err)
	}
	if d.Users == nil || d.Sessions == nil || d.APIKeys == nil || d.Hasher == nil || d.Tx == nil || d.Clock == nil || d.IDs == nil {
		return nil, errors.New("auth service requires user, session and api key stores, hasher, tx manager, clock and id generator")
	}
	if d.Random == nil {
		d.Random = rand.Reader
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{cfg: cfg, d: d, limiter: newLoginLimiter(cfg.LoginLimit, d.Clock)}, nil
}

// Kind says how a principal authenticated.
type Kind string

// Authentication kinds.
const (
	KindSession Kind = "session"
	KindAPIKey  Kind = "apiKey"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	User domain.User
	Kind Kind
	// Key is the API key used, for KindAPIKey.
	Key *domain.APIKey
	// CSRFToken is the session's CSRF token, for KindSession.
	CSRFToken string
}

// IsOwner reports whether the principal is the instance owner.
func (p Principal) IsOwner() bool { return p.User.Role == domain.RoleOwner }

func invalid(path, msg string) error {
	return &domain.ValidationError{Issues: []domain.Issue{{Path: path, Message: msg}}}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.d.Random, b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", invalid("email", "must be a valid email address")
	}
	return email, nil
}

func (s *Service) checkPassword(password string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < s.cfg.MinPasswordLength:
		return invalid("password", fmt.Sprintf("must have at least %d characters", s.cfg.MinPasswordLength))
	case n > s.cfg.MaxPasswordLength:
		return invalid("password", fmt.Sprintf("must have at most %d characters", s.cfg.MaxPasswordLength))
	}
	return nil
}

// NewUser is the data of an account to create.
type NewUser struct {
	Email    string
	Name     string
	Password string
}

func (s *Service) newUser(in NewUser, role domain.Role) (domain.User, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return domain.User{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.User{}, invalid("name", "must not be empty")
	}
	if err := s.checkPassword(in.Password); err != nil {
		return domain.User{}, err
	}
	hash, err := s.d.Hasher.Hash(in.Password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hashing password: %w", err)
	}
	id, err := s.d.IDs.UserID()
	if err != nil {
		return domain.User{}, fmt.Errorf("creating user id: %w", err)
	}
	return domain.User{ID: id, Email: email, Name: name, PasswordHash: hash, Role: role}, nil
}

// SetupRequired reports whether the first-run setup is still open: no user
// exists yet.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	n, err := s.d.Users.Count(ctx)
	if err != nil {
		return false, fmt.Errorf("checking setup state: %w", err)
	}
	return n == 0, nil
}

// Setup creates the owner account. It works exactly once: afterwards it fails
// with domain.ErrSetupDone, also when two setups race (the store allows one
// owner only).
func (s *Service) Setup(ctx context.Context, in NewUser) (domain.User, error) {
	open, err := s.SetupRequired(ctx)
	if err != nil {
		return domain.User{}, err
	}
	if !open {
		return domain.User{}, domain.ErrSetupDone
	}
	u, err := s.newUser(in, domain.RoleOwner)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.d.Users.Create(ctx, u); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.User{}, domain.ErrSetupDone
		}
		return domain.User{}, fmt.Errorf("creating owner: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = s.d.Clock.Now(), s.d.Clock.Now()
	s.d.Logger.InfoContext(ctx, "owner account created", "user", u.ID)
	return u, nil
}

// LoginResult is a new session. Token goes into the cookie, once.
type LoginResult struct {
	User      domain.User
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

// Login checks the credentials and opens a session. clientKey identifies the
// caller (its address) for rate limiting. Every failure but a rate limit is
// the same domain.ErrUnauthenticated, so callers learn nothing about accounts.
func (s *Service) Login(ctx context.Context, email, password, clientKey string) (LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	acctKey, addrKey := "acct:"+email, "addr:"+clientKey
	if err := s.limiter.check(acctKey, s.cfg.LoginLimit.MaxFailures); err != nil {
		return LoginResult{}, err
	}
	if err := s.limiter.check(addrKey, s.cfg.LoginLimit.MaxFailuresPerAddress); err != nil {
		return LoginResult{}, err
	}
	u, err := s.d.Users.GetByEmail(ctx, email)
	hash := u.PasswordHash
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return LoginResult{}, fmt.Errorf("loading user: %w", err)
		}
		hash = s.decoy.get(s.d.Hasher)
	}
	ok, verr := s.d.Hasher.Verify(password, hash)
	if verr != nil {
		s.d.Logger.WarnContext(ctx, "stored password hash is unusable", "user", u.ID, "error", verr)
	}
	if err != nil || verr != nil || !ok || u.Disabled {
		s.limiter.fail(acctKey)
		s.limiter.fail(addrKey)
		return LoginResult{}, domain.ErrUnauthenticated
	}
	s.limiter.reset(acctKey)
	if s.d.Hasher.NeedsRehash(u.PasswordHash) {
		if nh, err := s.d.Hasher.Hash(password); err == nil {
			u.PasswordHash = nh
			if err := s.d.Users.Update(ctx, u); err != nil {
				s.d.Logger.WarnContext(ctx, "upgrading password hash failed", "user", u.ID, "error", err)
			}
		}
	}
	return s.openSession(ctx, u)
}

func (s *Service) openSession(ctx context.Context, u domain.User) (LoginResult, error) {
	token, err := s.randomToken(s.cfg.Session.TokenBytes)
	if err != nil {
		return LoginResult{}, err
	}
	csrf, err := s.randomToken(s.cfg.Session.TokenBytes)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.d.Clock.Now()
	sess := domain.Session{
		TokenHash: hashToken(token), UserID: u.ID, CSRFToken: csrf,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.cfg.Session.AbsoluteTTL),
	}
	if err := s.d.Sessions.Create(ctx, sess); err != nil {
		return LoginResult{}, fmt.Errorf("creating session: %w", err)
	}
	return LoginResult{User: u, Token: token, CSRFToken: csrf, ExpiresAt: sess.ExpiresAt}, nil
}

// Logout ends the session of the token. An unknown token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.d.Sessions.Delete(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("closing session: %w", err)
	}
	return nil
}

// AuthenticateSession resolves a session token to its principal, or returns
// domain.ErrUnauthenticated when the session is unknown, expired (idle or
// absolute) or its user is gone or disabled. Use refreshes the idle timer.
func (s *Service) AuthenticateSession(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, domain.ErrUnauthenticated
	}
	hash := hashToken(token)
	sess, err := s.d.Sessions.Get(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("loading session: %w", err)
	}
	now := s.d.Clock.Now()
	if !now.Before(sess.ExpiresAt) || !now.Before(sess.LastSeenAt.Add(s.cfg.Session.IdleTTL)) {
		_ = s.d.Sessions.Delete(ctx, hash) // best effort; the purge job gets it otherwise
		return Principal{}, domain.ErrUnauthenticated
	}
	u, err := s.d.Users.Get(ctx, sess.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && u.Disabled) {
		return Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("loading session user: %w", err)
	}
	if now.Sub(sess.LastSeenAt) >= s.cfg.Session.TouchInterval {
		if err := s.d.Sessions.Touch(ctx, hash, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.d.Logger.WarnContext(ctx, "refreshing session failed", "error", err)
		}
	}
	return Principal{User: u, Kind: KindSession, CSRFToken: sess.CSRFToken}, nil
}

// AuthenticateAPIKey resolves an API key token to its principal. The caller
// must still check Key.Permits for the request.
func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) (Principal, error) {
	prefix, ok := s.keyPrefix(token)
	if !ok {
		return Principal{}, domain.ErrUnauthenticated
	}
	key, err := s.d.APIKeys.GetByPrefix(ctx, prefix)
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("loading api key: %w", err)
	}
	now := s.d.Clock.Now()
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(key.SecretHash)) != 1 || !key.Active(now) {
		return Principal{}, domain.ErrUnauthenticated
	}
	u, err := s.d.Users.Get(ctx, key.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && u.Disabled) {
		return Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("loading api key user: %w", err)
	}
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= s.cfg.APIKey.TouchInterval {
		if err := s.d.APIKeys.Touch(ctx, key.ID, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.d.Logger.WarnContext(ctx, "recording api key use failed", "key", key.ID, "error", err)
		}
	}
	return Principal{User: u, Kind: KindAPIKey, Key: &key}, nil
}

// IsAPIKey reports whether the token looks like an API key of this instance.
func (s *Service) IsAPIKey(token string) bool {
	_, ok := s.keyPrefix(token)
	return ok
}

// keyPrefix extracts the lookup prefix "<prefix>_<lookup>" of a key token.
func (s *Service) keyPrefix(token string) (string, bool) {
	rest, ok := strings.CutPrefix(token, s.cfg.APIKey.Prefix+"_")
	if !ok {
		return "", false
	}
	lookup, secret, ok := strings.Cut(rest, "_")
	if !ok || lookup == "" || secret == "" {
		return "", false
	}
	return s.cfg.APIKey.Prefix + "_" + lookup, true
}

// PurgeExpiredSessions deletes sessions past their absolute or idle expiry.
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	n, err := s.d.Sessions.DeleteExpired(ctx, now, now.Add(-s.cfg.Session.IdleTTL))
	if err != nil {
		return 0, fmt.Errorf("purging sessions: %w", err)
	}
	return n, nil
}

// Run purges expired sessions every auth.session.purgeInterval until ctx is
// done. The caller owns the goroutine.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Session.PurgeInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.PurgeExpiredSessions(ctx); err != nil {
				s.d.Logger.ErrorContext(ctx, "session purge failed", "error", err)
			} else if n > 0 {
				s.d.Logger.DebugContext(ctx, "expired sessions purged", "count", n)
			}
		}
	}
}
