package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/auth"
)

func TestSetup(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		in    auth.NewUser
		field string // expected validation path, empty for success
	}{
		{"ok", auth.NewUser{Email: " Owner@Example.com ", Name: "Ada", Password: goodPassword}, ""},
		{"bad email", auth.NewUser{Email: "nope", Name: "Ada", Password: goodPassword}, "email"},
		{"short password", auth.NewUser{Email: "a@b.io", Name: "Ada", Password: "short"}, "password"},
		{"long password", auth.NewUser{Email: "a@b.io", Name: "Ada", Password: strings.Repeat("x", 300)}, "password"},
		{"empty name", auth.NewUser{Email: "a@b.io", Name: "  ", Password: goodPassword}, "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			u, err := e.svc.Setup(ctx, tt.in)
			if tt.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				if u.Role != domain.RoleOwner || u.Email != "owner@example.com" || u.PasswordHash == "" || u.PasswordHash == goodPassword {
					t.Fatalf("user = %+v", u)
				}
				return
			}
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Issues[0].Path != tt.field {
				t.Fatalf("err = %v, want validation error on %q", err, tt.field)
			}
		})
	}
}

func TestSetupOnlyOnce(t *testing.T) {
	e := newEnv(t, nil)
	e.owner(t)
	_, err := e.svc.Setup(context.Background(), auth.NewUser{Email: "other@example.com", Name: "B", Password: goodPassword})
	if !errors.Is(err, domain.ErrSetupDone) {
		t.Fatalf("second setup = %v, want ErrSetupDone", err)
	}
	if open, _ := e.svc.SetupRequired(context.Background()); open {
		t.Fatal("setup must be closed")
	}
}

func TestLoginAndSessions(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(c *config.AuthConfig) {
		c.Session.IdleTTL, c.Session.AbsoluteTTL = time.Hour, 3*time.Hour
		c.Session.TouchInterval = time.Minute
	})
	_, owner := e.owner(t)

	res, err := e.svc.Login(ctx, "OWNER@example.com", goodPassword, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == "" || res.CSRFToken == "" || res.User.ID != owner.ID {
		t.Fatalf("login = %+v", res)
	}
	for _, s := range e.store.sessions {
		if s.TokenHash == res.Token {
			t.Fatal("session token stored in clear")
		}
	}
	p, err := e.svc.AuthenticateSession(ctx, res.Token)
	if err != nil || p.User.ID != owner.ID || p.Kind != auth.KindSession || p.CSRFToken != res.CSRFToken {
		t.Fatalf("authenticate = %+v, %v", p, err)
	}

	t.Run("idle expiry is sliding", func(t *testing.T) {
		e.clock.Advance(50 * time.Minute)
		if _, err := e.svc.AuthenticateSession(ctx, res.Token); err != nil {
			t.Fatalf("within idle window: %v", err)
		}
		e.clock.Advance(50 * time.Minute) // 100 min total, but only 50 since last use
		if _, err := e.svc.AuthenticateSession(ctx, res.Token); err != nil {
			t.Fatalf("activity must extend the idle window: %v", err)
		}
	})
	t.Run("absolute expiry", func(t *testing.T) {
		for range 3 { // stay active, still cut off at 3h
			e.clock.Advance(40 * time.Minute)
			_, err := e.svc.AuthenticateSession(ctx, res.Token)
			if e.clock.Now().Sub(res.ExpiresAt) >= 0 {
				if !errors.Is(err, domain.ErrUnauthenticated) {
					t.Fatalf("past absolute expiry: %v", err)
				}
				return
			}
		}
		t.Fatal("clock never passed the absolute expiry")
	})
	t.Run("idle timeout", func(t *testing.T) {
		r2, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "10.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(61 * time.Minute)
		if _, err := e.svc.AuthenticateSession(ctx, r2.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("idle session = %v, want ErrUnauthenticated", err)
		}
	})
	t.Run("logout", func(t *testing.T) {
		r3, _ := e.svc.Login(ctx, "owner@example.com", goodPassword, "10.0.0.1")
		if err := e.svc.Logout(ctx, r3.Token); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AuthenticateSession(ctx, r3.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("after logout = %v", err)
		}
		if err := e.svc.Logout(ctx, "unknown"); err != nil {
			t.Fatalf("unknown token logout must be fine: %v", err)
		}
	})
	t.Run("garbage token", func(t *testing.T) {
		for _, tok := range []string{"", "nope"} {
			if _, err := e.svc.AuthenticateSession(ctx, tok); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Errorf("AuthenticateSession(%q) = %v", tok, err)
			}
		}
	})
}

func TestLoginFailuresAreUniform(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	_, owner := e.owner(t)
	disabled := true
	ownerP := auth.Principal{User: owner, Kind: auth.KindSession}
	m, err := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "m@example.com", Name: "M", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateUser(ctx, ownerP, m.ID, auth.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, email, pw string }{
		{"wrong password", "owner@example.com", "wrong password!"},
		{"unknown user", "ghost@example.com", goodPassword},
		{"disabled user", "m@example.com", goodPassword},
	} {
		_, err := e.svc.Login(ctx, c.email, c.pw, "10.0.0.2")
		if !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want ErrUnauthenticated", c.name, err)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(c *config.AuthConfig) {
		c.LoginLimit = config.LoginLimitConfig{Enabled: true, MaxFailures: 3, MaxFailuresPerAddress: 5, Window: 10 * time.Minute, MaxTrackedKeys: 100}
	})
	e.owner(t)
	for i := range 3 {
		if _, err := e.svc.Login(ctx, "owner@example.com", "wrong password!", "1.1.1.1"); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("attempt %d = %v", i, err)
		}
	}
	// The account is locked even for the right password, from any address.
	_, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "2.2.2.2")
	var rl *domain.RateLimitedError
	if !errors.As(err, &rl) || !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if rl.RetryAfter <= 0 || rl.RetryAfter > 10*time.Minute {
		t.Fatalf("RetryAfter = %v", rl.RetryAfter)
	}
	e.clock.Advance(11 * time.Minute)
	if _, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "2.2.2.2"); err != nil {
		t.Fatalf("after the window: %v", err)
	}

	t.Run("address limit spans accounts", func(t *testing.T) {
		for i := range 5 {
			_, _ = e.svc.Login(ctx, strings.Repeat("a", i+1)+"@example.com", "x", "9.9.9.9")
		}
		_, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "9.9.9.9")
		if !errors.Is(err, domain.ErrRateLimited) {
			t.Fatalf("err = %v, want rate limited by address", err)
		}
	})
	t.Run("success resets the account counter", func(t *testing.T) {
		e2 := newEnv(t, func(c *config.AuthConfig) {
			c.LoginLimit = config.LoginLimitConfig{Enabled: true, MaxFailures: 3, MaxFailuresPerAddress: 50, Window: time.Hour, MaxTrackedKeys: 100}
		})
		e2.ownerCtx(ctx, t)
		for range 5 {
			_, _ = e2.svc.Login(ctx, "owner@example.com", "wrong password!", "3.3.3.3")
			if _, err := e2.svc.Login(ctx, "owner@example.com", goodPassword, "3.3.3.3"); err != nil {
				// The first loop iteration may already be blocked only after 3 failures in a row; we never reach that.
				t.Fatalf("login failed: %v", err)
			}
		}
	})
	t.Run("disabled limiter", func(t *testing.T) {
		e3 := newEnv(t, func(c *config.AuthConfig) { c.LoginLimit.Enabled = false })
		e3.ownerCtx(ctx, t)
		for range 30 {
			_, _ = e3.svc.Login(ctx, "owner@example.com", "wrong password!", "4.4.4.4")
		}
		if _, err := e3.svc.Login(ctx, "owner@example.com", goodPassword, "4.4.4.4"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPasswordRehashOnLogin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	_, owner := e.owner(t)
	old := e.store.users[owner.ID]
	old.PasswordHash = "v0:" + goodPassword // made with older parameters
	e.store.users[owner.ID] = old
	if _, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "ip"); err != nil {
		t.Fatal(err)
	}
	if got := e.store.users[owner.ID].PasswordHash; got != "v1:"+goodPassword {
		t.Fatalf("hash after login = %q, want upgraded", got)
	}
}

func TestUserManagement(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	ownerP, owner := e.owner(t)
	m, err := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "m@example.com", Name: "M", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	member := auth.Principal{User: m, Kind: auth.KindSession}
	other, _ := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "o@example.com", Name: "O", Password: goodPassword})

	t.Run("member cannot manage users", func(t *testing.T) {
		if _, err := e.svc.CreateMember(ctx, member, auth.NewUser{Email: "x@example.com", Name: "X", Password: goodPassword}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("CreateMember = %v", err)
		}
		if _, err := e.svc.ListUsers(ctx, member, port.Page{}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("ListUsers = %v", err)
		}
		if _, err := e.svc.GetUser(ctx, member, other.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("GetUser(other) = %v", err)
		}
		if _, err := e.svc.GetUser(ctx, member, m.ID); err != nil {
			t.Errorf("GetUser(self) = %v", err)
		}
		if err := e.svc.DeleteUser(ctx, member, other.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("DeleteUser = %v", err)
		}
		name := "Hacked"
		if _, err := e.svc.UpdateUser(ctx, member, other.ID, auth.UserPatch{Name: &name}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("UpdateUser(other) = %v", err)
		}
		dis := true
		if _, err := e.svc.UpdateUser(ctx, member, m.ID, auth.UserPatch{Disabled: &dis}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("self-disable = %v", err)
		}
	})
	t.Run("duplicate email", func(t *testing.T) {
		if _, err := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "M@example.com", Name: "M2", Password: goodPassword}); !errors.Is(err, domain.ErrAlreadyExists) {
			t.Errorf("err = %v, want ErrAlreadyExists", err)
		}
	})
	t.Run("password change needs the current password and ends sessions", func(t *testing.T) {
		login, err := e.svc.Login(ctx, "m@example.com", goodPassword, "ip")
		if err != nil {
			t.Fatal(err)
		}
		next := "an entirely new passphrase"
		var ve *domain.ValidationError
		if _, err := e.svc.UpdateUser(ctx, member, m.ID, auth.UserPatch{Password: &next, CurrentPassword: "wrong"}); !errors.As(err, &ve) {
			t.Fatalf("wrong current password = %v", err)
		}
		if _, err := e.svc.UpdateUser(ctx, member, m.ID, auth.UserPatch{Password: &next, CurrentPassword: goodPassword}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AuthenticateSession(ctx, login.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("old session survived a password change: %v", err)
		}
		if _, err := e.svc.Login(ctx, "m@example.com", next, "ip"); err != nil {
			t.Fatalf("login with the new password: %v", err)
		}
	})
	t.Run("owner disables and deletes", func(t *testing.T) {
		dis := true
		if _, err := e.svc.UpdateUser(ctx, ownerP, other.ID, auth.UserPatch{Disabled: &dis}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Login(ctx, "o@example.com", goodPassword, "ip"); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("disabled login = %v", err)
		}
		if err := e.svc.DeleteUser(ctx, ownerP, other.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.DeleteUser(ctx, ownerP, other.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("second delete = %v", err)
		}
		if err := e.svc.DeleteUser(ctx, ownerP, owner.ID); err == nil {
			t.Fatal("the owner must not be deletable")
		}
	})
	t.Run("list", func(t *testing.T) {
		res, err := e.svc.ListUsers(ctx, ownerP, port.Page{})
		if err != nil || len(res.Items) != 2 {
			t.Fatalf("ListUsers = %d, %v", len(res.Items), err)
		}
	})
}

func TestAPIKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, func(c *config.AuthConfig) { c.APIKey.MaxPerUser = 2 })
	ownerP, owner := e.owner(t)

	created, err := e.svc.CreateAPIKey(ctx, ownerP, auth.NewAPIKey{Name: "ci", Scopes: []domain.Scope{domain.ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "engi_") || !strings.HasPrefix(created.Token, created.Key.Prefix+"_") {
		t.Fatalf("token = %q, prefix = %q", created.Token, created.Key.Prefix)
	}
	if created.Key.SecretHash == "" || strings.Contains(created.Key.SecretHash, created.Token) {
		t.Fatal("token must be stored hashed")
	}
	if !e.svc.IsAPIKey(created.Token) || e.svc.IsAPIKey("plain-session-token") {
		t.Fatal("IsAPIKey misclassifies tokens")
	}

	p, err := e.svc.AuthenticateAPIKey(ctx, created.Token)
	if err != nil || p.Kind != auth.KindAPIKey || p.User.ID != owner.ID || p.Key == nil || !p.Key.Permits(false) || p.Key.Permits(true) {
		t.Fatalf("authenticate = %+v, %v", p, err)
	}

	t.Run("wrong secret", func(t *testing.T) {
		bad := created.Key.Prefix + "_" + strings.Repeat("A", 43)
		for _, tok := range []string{bad, created.Key.Prefix, "engi_x_y", "other_" + created.Token, ""} {
			if _, err := e.svc.AuthenticateAPIKey(ctx, tok); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Errorf("token %q = %v", tok, err)
			}
		}
	})
	t.Run("keys cannot mint keys", func(t *testing.T) {
		if _, err := e.svc.CreateAPIKey(ctx, p, auth.NewAPIKey{Name: "child"}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})
	t.Run("validation", func(t *testing.T) {
		past := e.clock.Now().Add(-time.Hour)
		for name, in := range map[string]auth.NewAPIKey{
			"no name":       {Name: " "},
			"unknown scope": {Name: "x", Scopes: []domain.Scope{"admin"}},
			"past expiry":   {Name: "x", ExpiresAt: &past},
		} {
			var ve *domain.ValidationError
			if _, err := e.svc.CreateAPIKey(ctx, ownerP, in); !errors.As(err, &ve) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("expiry", func(t *testing.T) {
		exp := e.clock.Now().Add(time.Hour)
		k, err := e.svc.CreateAPIKey(ctx, ownerP, auth.NewAPIKey{Name: "short", ExpiresAt: &exp})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AuthenticateAPIKey(ctx, k.Token); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(2 * time.Hour)
		if _, err := e.svc.AuthenticateAPIKey(ctx, k.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("expired key = %v", err)
		}
	})
	t.Run("limit", func(t *testing.T) {
		if _, err := e.svc.CreateAPIKey(ctx, ownerP, auth.NewAPIKey{Name: "second"}); err != nil {
			t.Fatal(err) // the first key is active; the expired one does not count
		}
		var ve *domain.ValidationError
		if _, err := e.svc.CreateAPIKey(ctx, ownerP, auth.NewAPIKey{Name: "third"}); !errors.As(err, &ve) {
			t.Errorf("err = %v, want limit validation error", err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		if err := e.svc.RevokeAPIKey(ctx, ownerP, created.Key.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AuthenticateAPIKey(ctx, created.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("revoked key = %v", err)
		}
		if err := e.svc.RevokeAPIKey(ctx, ownerP, created.Key.ID); err != nil {
			t.Fatalf("revoking twice: %v", err)
		}
		if err := e.svc.RevokeAPIKey(ctx, ownerP, "ak_missing"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("unknown key = %v", err)
		}
		res, _ := e.svc.ListAPIKeys(ctx, ownerP, port.Page{})
		revoked := 0
		for _, k := range res.Items {
			if k.RevokedAt != nil {
				revoked++
			}
		}
		if revoked != 1 {
			t.Fatalf("revoked keys listed = %d, want 1", revoked)
		}
	})
	t.Run("a member cannot revoke someone else's key", func(t *testing.T) {
		m, _ := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "m@example.com", Name: "M", Password: goodPassword})
		k, _ := e.svc.CreateAPIKey(ctx, ownerP, auth.NewAPIKey{Name: "mine"})
		err := e.svc.RevokeAPIKey(ctx, auth.Principal{User: m, Kind: auth.KindSession}, k.Key.ID)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("deleted user", func(t *testing.T) {
		m, _ := e.svc.CreateMember(ctx, ownerP, auth.NewUser{Email: "z@example.com", Name: "Z", Password: goodPassword})
		k, err := e.svc.CreateAPIKey(ctx, auth.Principal{User: m, Kind: auth.KindSession}, auth.NewAPIKey{Name: "z"})
		if err != nil {
			t.Fatal(err)
		}
		delete(e.store.users, m.ID)
		if _, err := e.svc.AuthenticateAPIKey(ctx, k.Token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("key of deleted user = %v", err)
		}
	})
}

func TestPurgeExpiredSessions(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, nil)
	e.owner(t)
	if _, err := e.svc.Login(ctx, "owner@example.com", goodPassword, "ip"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.PurgeExpiredSessions(ctx); n != 0 {
		t.Fatalf("purged %d fresh sessions", n)
	}
	e.clock.Advance(config.Default().Auth.Session.AbsoluteTTL + time.Hour)
	if n, err := e.svc.PurgeExpiredSessions(ctx); err != nil || n != 1 {
		t.Fatalf("purged %d, %v; want 1", n, err)
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := auth.New(config.Default().Auth, auth.Deps{}); err == nil {
		t.Fatal("expected an error without dependencies")
	}
	bad := config.Default().Auth
	bad.Session.IdleTTL = 0
	if _, err := auth.New(bad, auth.Deps{}); err == nil {
		t.Fatal("expected an error for invalid config")
	}
}
