package auth_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/auth"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

// memStore implements the three auth stores in memory.
type memStore struct {
	mu       sync.Mutex
	users    map[domain.UserID]domain.User
	sessions map[string]domain.Session
	keys     map[domain.APIKeyID]domain.APIKey
	touches  int
}

func newMemStore() *memStore {
	return &memStore{users: map[domain.UserID]domain.User{}, sessions: map[string]domain.Session{}, keys: map[domain.APIKeyID]domain.APIKey{}}
}

type users struct{ *memStore }
type sessions struct{ *memStore }
type apiKeys struct{ *memStore }

func nf(what string) error { return fmt.Errorf("%s: %w", what, domain.ErrNotFound) }

func (u users) Create(_ context.Context, usr domain.User) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, o := range u.users {
		if o.Email == usr.Email || (o.Role == domain.RoleOwner && usr.Role == domain.RoleOwner) {
			return domain.ErrAlreadyExists
		}
	}
	u.users[usr.ID] = usr
	return nil
}
func (u users) Get(_ context.Context, i domain.UserID) (domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x, ok := u.users[i]; ok {
		return x, nil
	}
	return domain.User{}, nf("user")
}
func (u users) GetByEmail(_ context.Context, e string) (domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.users {
		if x.Email == e {
			return x, nil
		}
	}
	return domain.User{}, nf("user")
}
func (u users) Update(_ context.Context, usr domain.User) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.users[usr.ID]; !ok {
		return nf("user")
	}
	u.users[usr.ID] = usr
	return nil
}
func (u users) Delete(_ context.Context, i domain.UserID) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.users[i]; !ok {
		return nf("user")
	}
	delete(u.users, i)
	return nil
}
func (u users) List(_ context.Context, _ port.Page) (port.PageResult[domain.User], error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []domain.User
	for _, x := range u.users {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return port.PageResult[domain.User]{Items: out}, nil
}
func (u users) Count(context.Context) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.users), nil
}

func (s sessions) Create(_ context.Context, x domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[x.TokenHash] = x
	return nil
}
func (s sessions) Get(_ context.Context, h string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x, ok := s.sessions[h]; ok {
		return x, nil
	}
	return domain.Session{}, nf("session")
}
func (s sessions) Touch(_ context.Context, h string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.sessions[h]
	if !ok {
		return nf("session")
	}
	x.LastSeenAt = t
	s.sessions[h] = x
	s.touches++
	return nil
}
func (s sessions) Delete(_ context.Context, h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, h)
	return nil
}
func (s sessions) DeleteByUser(_ context.Context, i domain.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, x := range s.sessions {
		if x.UserID == i {
			delete(s.sessions, h)
		}
	}
	return nil
}
func (s sessions) DeleteExpired(_ context.Context, now, idle time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for h, x := range s.sessions {
		if !x.ExpiresAt.After(now) || x.LastSeenAt.Before(idle) {
			delete(s.sessions, h)
			n++
		}
	}
	return n, nil
}

func (a apiKeys) Create(_ context.Context, k domain.APIKey) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[k.ID] = k
	return nil
}
func (a apiKeys) GetByPrefix(_ context.Context, p string) (domain.APIKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range a.keys {
		if k.Prefix == p {
			return k, nil
		}
	}
	return domain.APIKey{}, nf("api key")
}
func (a apiKeys) Get(_ context.Context, i domain.APIKeyID) (domain.APIKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k, ok := a.keys[i]; ok {
		return k, nil
	}
	return domain.APIKey{}, nf("api key")
}
func (a apiKeys) ListByUser(_ context.Context, u domain.UserID, _ port.Page) (port.PageResult[domain.APIKey], error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []domain.APIKey
	for _, k := range a.keys {
		if k.UserID == u {
			out = append(out, k)
		}
	}
	return port.PageResult[domain.APIKey]{Items: out}, nil
}
func (a apiKeys) CountActive(_ context.Context, u domain.UserID, now time.Time) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, k := range a.keys {
		if k.UserID == u && k.Active(now) {
			n++
		}
	}
	return n, nil
}
func (a apiKeys) Revoke(_ context.Context, i domain.APIKeyID, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	k, ok := a.keys[i]
	if !ok {
		return nf("api key")
	}
	if k.RevokedAt == nil {
		k.RevokedAt = &at
	}
	a.keys[i] = k
	return nil
}
func (a apiKeys) Touch(_ context.Context, i domain.APIKeyID, at time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	k, ok := a.keys[i]
	if !ok {
		return nf("api key")
	}
	k.LastUsedAt = &at
	a.keys[i] = k
	return nil
}

// plainHasher is a fast, deterministic stand-in for argon2id.
type plainHasher struct{ version string }

func (h plainHasher) Hash(p string) (string, error) { return h.version + ":" + p, nil }
func (h plainHasher) Verify(p, e string) (bool, error) {
	if !strings.Contains(e, ":") {
		return false, fmt.Errorf("bad hash")
	}
	return strings.TrimPrefix(e[strings.IndexByte(e, ':'):], ":") == p, nil
}
func (h plainHasher) NeedsRehash(e string) bool { return !strings.HasPrefix(e, h.version+":") }

type noTx struct{}

func (noTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type env struct {
	svc   *auth.Service
	store *memStore
	clock *servicetest.Clock
}

func newEnv(t *testing.T, mutate func(*config.AuthConfig)) *env {
	t.Helper()
	cfg := config.Default().Auth
	if mutate != nil {
		mutate(&cfg)
	}
	clk := servicetest.NewClock()
	st := newMemStore()
	svc, err := auth.New(cfg, auth.Deps{
		Users: users{st}, Sessions: sessions{st}, APIKeys: apiKeys{st}, Hasher: plainHasher{"v1"}, Tx: noTx{},
		Clock: clk, IDs: id.NewGenerator(clk.Now, rand.Reader),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, store: st, clock: clk}
}

const goodPassword = "correct horse battery"

func (e *env) owner(t *testing.T) (auth.Principal, domain.User) {
	t.Helper()
	return e.ownerCtx(t.Context(), t)
}

func (e *env) ownerCtx(ctx context.Context, t *testing.T) (auth.Principal, domain.User) {
	t.Helper()
	u, err := e.svc.Setup(ctx, auth.NewUser{Email: "Owner@Example.com", Name: "Owner", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	return auth.Principal{User: u, Kind: auth.KindSession}, u
}
