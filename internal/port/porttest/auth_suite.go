package porttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// AuthStores is the set of authentication ports a persistence adapter provides.
type AuthStores struct {
	Users    port.UserStore
	Sessions port.SessionStore
	APIKeys  port.APIKeyStore
}

// AuthStoreFactory builds fresh, empty, migrated stores that read time from
// clk. It registers its own cleanup with t.
type AuthStoreFactory func(t *testing.T, clk port.Clock) AuthStores

type authEnv struct {
	AuthStores
	fx  *fixtures
	clk *FakeClock
}

// RunAuthSuite runs the user, session and API key contract tests.
func RunAuthSuite(t *testing.T, newStores AuthStoreFactory) {
	t.Helper()
	cases := map[string]func(*testing.T, *authEnv){
		"UserRoundTrip":        testUserRoundTrip,
		"UserEmailIsUnique":    testUserEmailUnique,
		"SingleOwner":          testSingleOwner,
		"UserUpdateDelete":     testUserUpdateDelete,
		"UserPagination":       testUserPagination,
		"SessionLifecycle":     testSessionLifecycle,
		"SessionExpiry":        testSessionExpiry,
		"SessionsFollowUser":   testSessionsFollowUser,
		"APIKeyLifecycle":      testAPIKeyLifecycle,
		"APIKeyUniquePrefix":   testAPIKeyUniquePrefix,
		"APIKeyListAndCount":   testAPIKeyListAndCount,
		"APIKeysFollowUser":    testAPIKeysFollowUser,
		"APIKeyNotFoundErrors": testAPIKeyNotFound,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			clk := NewFakeClock()
			fn(t, &authEnv{AuthStores: newStores(t, clk), fx: newFixtures(t, clk), clk: clk})
		})
	}
}

func (e *authEnv) user(t *testing.T, email string, role domain.Role) domain.User {
	t.Helper()
	id, err := e.fx.gen.UserID()
	if err != nil {
		t.Fatal(err)
	}
	u := domain.User{ID: id, Email: email, Name: "N " + email, PasswordHash: "hash", Role: role}
	if err := e.Users.Create(context.Background(), u); err != nil {
		t.Fatalf("Create(%s): %v", email, err)
	}
	return u
}

func (e *authEnv) key(t *testing.T, owner domain.UserID, prefix string) domain.APIKey {
	t.Helper()
	id, err := e.fx.gen.APIKeyID()
	if err != nil {
		t.Fatal(err)
	}
	k := domain.APIKey{ID: id, UserID: owner, Name: "k " + prefix, Prefix: prefix, SecretHash: "h-" + prefix,
		Scopes: []domain.Scope{domain.ScopeRead, domain.ScopeWrite}, CreatedAt: e.clk.Now()}
	if err := e.APIKeys.Create(context.Background(), k); err != nil {
		t.Fatalf("Create key %s: %v", prefix, err)
	}
	return k
}

func testUserRoundTrip(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "a@example.com", domain.RoleOwner)
	got, err := e.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != u.Email || got.Name != u.Name || got.PasswordHash != "hash" || got.Role != domain.RoleOwner || got.Disabled {
		t.Fatalf("Get = %+v", got)
	}
	if !got.CreatedAt.Equal(e.clk.Now()) || !got.UpdatedAt.Equal(e.clk.Now()) {
		t.Fatalf("timestamps = %v / %v, want %v", got.CreatedAt, got.UpdatedAt, e.clk.Now())
	}
	byEmail, err := e.Users.GetByEmail(ctx, "a@example.com")
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("GetByEmail = %+v, %v", byEmail, err)
	}
	if _, err := e.Users.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByEmail(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := e.Users.Get(ctx, "us_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get(unknown) = %v, want ErrNotFound", err)
	}
	if n, err := e.Users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("Count = %d, %v", n, err)
	}
}

func testUserEmailUnique(t *testing.T, e *authEnv) {
	e.user(t, "dup@example.com", domain.RoleMember)
	id, _ := e.fx.gen.UserID()
	err := e.Users.Create(context.Background(), domain.User{ID: id, Email: "dup@example.com", Name: "x", PasswordHash: "h", Role: domain.RoleMember})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate email = %v, want ErrAlreadyExists", err)
	}
}

func testSingleOwner(t *testing.T, e *authEnv) {
	e.user(t, "owner@example.com", domain.RoleOwner)
	id, _ := e.fx.gen.UserID()
	err := e.Users.Create(context.Background(), domain.User{ID: id, Email: "second@example.com", Name: "x", PasswordHash: "h", Role: domain.RoleOwner})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second owner = %v, want ErrAlreadyExists", err)
	}
	e.user(t, "member@example.com", domain.RoleMember)
	e.user(t, "member2@example.com", domain.RoleMember)
}

func testUserUpdateDelete(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "u@example.com", domain.RoleMember)
	other := e.user(t, "other@example.com", domain.RoleMember)
	e.clk.Advance(time.Hour)
	u.Name, u.Disabled, u.PasswordHash = "Renamed", true, "newhash"
	if err := e.Users.Update(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Users.Get(ctx, u.ID)
	if got.Name != "Renamed" || !got.Disabled || got.PasswordHash != "newhash" || !got.UpdatedAt.Equal(e.clk.Now()) {
		t.Fatalf("after update = %+v", got)
	}
	u.Email = other.Email
	if err := e.Users.Update(ctx, u); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("update to taken email = %v, want ErrAlreadyExists", err)
	}
	if err := e.Users.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Users.Delete(ctx, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if err := e.Users.Update(ctx, u); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update after delete = %v, want ErrNotFound", err)
	}
}

func testUserPagination(t *testing.T, e *authEnv) {
	ctx := context.Background()
	var ids []domain.UserID
	for _, em := range []string{"1@x.io", "2@x.io", "3@x.io", "4@x.io", "5@x.io"} {
		ids = append(ids, e.user(t, em, domain.RoleMember).ID)
	}
	var seen []domain.UserID
	cursor := ""
	for range 5 {
		res, err := e.Users.List(ctx, port.Page{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range res.Items {
			seen = append(seen, u.ID)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d users, want 5", len(seen))
	}
	for i, id := range seen { // newest first
		if id != ids[len(ids)-1-i] {
			t.Fatalf("order[%d] = %s, want %s", i, id, ids[len(ids)-1-i])
		}
	}
}

func (e *authEnv) session(t *testing.T, u domain.UserID, hash string) domain.Session {
	t.Helper()
	s := domain.Session{TokenHash: hash, UserID: u, CSRFToken: "csrf-" + hash,
		CreatedAt: e.clk.Now(), LastSeenAt: e.clk.Now(), ExpiresAt: e.clk.Now().Add(24 * time.Hour)}
	if err := e.Sessions.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testSessionLifecycle(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "s@example.com", domain.RoleOwner)
	want := e.session(t, u.ID, "h1")
	got, err := e.Sessions.Get(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != u.ID || got.CSRFToken != "csrf-h1" || !got.ExpiresAt.Equal(want.ExpiresAt) || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
	e.clk.Advance(time.Minute)
	if err := e.Sessions.Touch(ctx, "h1", e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.Sessions.Get(ctx, "h1"); !got.LastSeenAt.Equal(e.clk.Now()) {
		t.Fatalf("LastSeenAt = %v, want %v", got.LastSeenAt, e.clk.Now())
	}
	if err := e.Sessions.Touch(ctx, "nope", e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Touch(unknown) = %v, want ErrNotFound", err)
	}
	if err := e.Sessions.Delete(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := e.Sessions.Delete(ctx, "h1"); err != nil {
		t.Fatalf("deleting twice must be fine: %v", err)
	}
	if _, err := e.Sessions.Get(ctx, "h1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func testSessionExpiry(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "s@example.com", domain.RoleOwner)
	e.session(t, u.ID, "fresh")
	idle := e.session(t, u.ID, "idle")
	expired := domain.Session{TokenHash: "expired", UserID: u.ID, CSRFToken: "c", CreatedAt: e.clk.Now(), LastSeenAt: e.clk.Now(), ExpiresAt: e.clk.Now().Add(time.Minute)}
	if err := e.Sessions.Create(ctx, expired); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(2 * time.Hour)
	if err := e.Sessions.Touch(ctx, "fresh", e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	// "idle" was last seen 2h ago (cutoff 1h), "expired" is past its absolute expiry.
	n, err := e.Sessions.DeleteExpired(ctx, e.clk.Now(), e.clk.Now().Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("DeleteExpired = %d, %v; want 2", n, err)
	}
	if _, err := e.Sessions.Get(ctx, idle.TokenHash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("idle session survived: %v", err)
	}
	if _, err := e.Sessions.Get(ctx, "fresh"); err != nil {
		t.Fatalf("fresh session was deleted: %v", err)
	}
}

func testSessionsFollowUser(t *testing.T, e *authEnv) {
	ctx := context.Background()
	a := e.user(t, "a@example.com", domain.RoleOwner)
	b := e.user(t, "b@example.com", domain.RoleMember)
	e.session(t, a.ID, "a1")
	e.session(t, a.ID, "a2")
	e.session(t, b.ID, "b1")
	if err := e.Sessions.DeleteByUser(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"a1", "a2"} {
		if _, err := e.Sessions.Get(ctx, h); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("session %s survived DeleteByUser", h)
		}
	}
	if _, err := e.Sessions.Get(ctx, "b1"); err != nil {
		t.Fatalf("other user's session removed: %v", err)
	}
	if err := e.Users.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sessions.Get(ctx, "b1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("session must be removed with its user: %v", err)
	}
}

func testAPIKeyLifecycle(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "k@example.com", domain.RoleOwner)
	exp := e.clk.Now().Add(time.Hour)
	id, _ := e.fx.gen.APIKeyID()
	k := domain.APIKey{ID: id, UserID: u.ID, Name: "ci", Prefix: "engi_abc", SecretHash: "sh",
		Scopes: []domain.Scope{domain.ScopeRead}, ExpiresAt: &exp, CreatedAt: e.clk.Now()}
	if err := e.APIKeys.Create(ctx, k); err != nil {
		t.Fatal(err)
	}
	got, err := e.APIKeys.GetByPrefix(ctx, "engi_abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.UserID != u.ID || got.Name != "ci" || got.SecretHash != "sh" ||
		len(got.Scopes) != 1 || got.Scopes[0] != domain.ScopeRead || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) ||
		got.LastUsedAt != nil || got.RevokedAt != nil {
		t.Fatalf("GetByPrefix = %+v", got)
	}
	byID, err := e.APIKeys.Get(ctx, id)
	if err != nil || byID.Prefix != "engi_abc" {
		t.Fatalf("Get = %+v, %v", byID, err)
	}
	e.clk.Advance(time.Minute)
	if err := e.APIKeys.Touch(ctx, id, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	first := e.clk.Now()
	if err := e.APIKeys.Revoke(ctx, id, first); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Minute)
	if err := e.APIKeys.Revoke(ctx, id, e.clk.Now()); err != nil {
		t.Fatalf("revoking twice must be fine: %v", err)
	}
	got, _ = e.APIKeys.Get(ctx, id)
	if got.RevokedAt == nil || !got.RevokedAt.Equal(first) {
		t.Fatalf("RevokedAt = %v, want the first revocation %v", got.RevokedAt, first)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(first) {
		t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, first)
	}
}

func testAPIKeyUniquePrefix(t *testing.T, e *authEnv) {
	u := e.user(t, "k@example.com", domain.RoleOwner)
	e.key(t, u.ID, "engi_same")
	id, _ := e.fx.gen.APIKeyID()
	err := e.APIKeys.Create(context.Background(), domain.APIKey{ID: id, UserID: u.ID, Name: "n", Prefix: "engi_same", SecretHash: "x",
		Scopes: []domain.Scope{domain.ScopeRead}, CreatedAt: e.clk.Now()})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate prefix = %v, want ErrAlreadyExists", err)
	}
}

func testAPIKeyListAndCount(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "k@example.com", domain.RoleOwner)
	v := e.user(t, "v@example.com", domain.RoleMember)
	k1 := e.key(t, u.ID, "p1")
	e.key(t, u.ID, "p2")
	k3 := e.key(t, u.ID, "p3")
	e.key(t, v.ID, "q1")
	if err := e.APIKeys.Revoke(ctx, k1.ID, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	exp := e.clk.Now().Add(time.Minute)
	expiring := domain.APIKey{ID: k3.ID + "x", UserID: u.ID, Name: "e", Prefix: "p4", SecretHash: "x", Scopes: []domain.Scope{domain.ScopeRead}, ExpiresAt: &exp, CreatedAt: e.clk.Now()}
	if id, err := e.fx.gen.APIKeyID(); err == nil {
		expiring.ID = id
	}
	if err := e.APIKeys.Create(ctx, expiring); err != nil {
		t.Fatal(err)
	}
	res, err := e.APIKeys.ListByUser(ctx, u.ID, port.Page{Limit: 2})
	if err != nil || len(res.Items) != 2 || res.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q, %v", len(res.Items), res.NextCursor, err)
	}
	rest, err := e.APIKeys.ListByUser(ctx, u.ID, port.Page{Cursor: res.NextCursor, Limit: 10})
	if err != nil || len(rest.Items) != 2 || rest.NextCursor != "" {
		t.Fatalf("second page = %d items, cursor %q, %v", len(rest.Items), rest.NextCursor, err)
	}
	if n, err := e.APIKeys.CountActive(ctx, u.ID, e.clk.Now()); err != nil || n != 3 {
		t.Fatalf("CountActive = %d, %v; want 3 (p1 revoked)", n, err)
	}
	if n, _ := e.APIKeys.CountActive(ctx, u.ID, e.clk.Now().Add(time.Hour)); n != 2 {
		t.Fatalf("CountActive after expiry = %d, want 2", n)
	}
}

func testAPIKeysFollowUser(t *testing.T, e *authEnv) {
	ctx := context.Background()
	u := e.user(t, "k@example.com", domain.RoleMember)
	k := e.key(t, u.ID, "gone")
	if err := e.Users.Delete(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.APIKeys.Get(ctx, k.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("key must be removed with its user: %v", err)
	}
}

func testAPIKeyNotFound(t *testing.T, e *authEnv) {
	ctx := context.Background()
	if _, err := e.APIKeys.GetByPrefix(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByPrefix = %v", err)
	}
	if err := e.APIKeys.Revoke(ctx, "ak_nope", e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Revoke = %v", err)
	}
	if err := e.APIKeys.Touch(ctx, "ak_nope", e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Touch = %v", err)
	}
}
