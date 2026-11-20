package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/auth"
)

// principalFrom returns the authenticated caller stored by authenticated.
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(auth.Principal)
	return p, ok
}

// mustPrincipal returns the caller of an authenticated route.
func mustPrincipal(r *http.Request) (auth.Principal, error) {
	p, ok := principalFrom(r.Context())
	if !ok {
		return auth.Principal{}, fmt.Errorf("no principal on an authenticated route: %w", domain.ErrUnauthenticated)
	}
	return p, nil
}

// isSafe reports whether the method only reads.
func isSafe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// readsOnly reports whether the request changes nothing: a safe method, or the
// expression preview, which is a POST only because it carries a body.
func readsOnly(r *http.Request) bool {
	return isSafe(r.Method) || r.URL.Path == v1("/expressions/preview")
}

// bearerToken extracts the token of an "Authorization: Bearer" header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const scheme = "bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	return strings.TrimSpace(h[len(scheme):]), true
}

// authenticated wraps a handler with authentication, API key scope checks and
// CSRF protection for cookie-authenticated changes.
func (a *API) authenticated(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, err := a.authenticate(r)
		if err != nil {
			return err
		}
		return next(w, r.WithContext(contextWithPrincipal(r, p)))
	}
}

func (a *API) authenticate(r *http.Request) (auth.Principal, error) {
	ctx := r.Context()
	if token, ok := bearerToken(r); ok {
		if !a.d.Auth.IsAPIKey(token) {
			return auth.Principal{}, fmt.Errorf("bearer token is not an API key: %w", domain.ErrUnauthenticated)
		}
		p, err := a.d.Auth.AuthenticateAPIKey(ctx, token)
		if err != nil {
			return auth.Principal{}, fmt.Errorf("authenticating API key: %w", err)
		}
		if p.Key != nil && !p.Key.Permits(!readsOnly(r)) {
			return auth.Principal{}, fmt.Errorf("the API key lacks the write scope: %w", domain.ErrForbidden)
		}
		return p, nil
	}
	c, err := r.Cookie(a.d.Session.CookieName)
	if err != nil || c.Value == "" {
		return auth.Principal{}, fmt.Errorf("no credentials: %w", domain.ErrUnauthenticated)
	}
	p, err := a.d.Auth.AuthenticateSession(ctx, c.Value)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("authenticating session: %w", err)
	}
	if !isSafe(r.Method) {
		got := r.Header.Get(a.d.Session.CSRFHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRFToken)) != 1 {
			return auth.Principal{}, fmt.Errorf("missing or wrong %s header: %w", a.d.Session.CSRFHeader, domain.ErrForbidden)
		}
	}
	return p, nil
}

func contextWithPrincipal(r *http.Request, p auth.Principal) context.Context {
	if info, ok := r.Context().Value(ctxInfo).(*reqInfo); ok {
		info.user = p.User.ID
	}
	return context.WithValue(r.Context(), ctxPrincipal, p)
}
