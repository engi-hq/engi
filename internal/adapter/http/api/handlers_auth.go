package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/auth"
)

// forwardedProtoHeader carries the scheme a proxy saw. It is only read when
// server.trustForwardedFor says the proxy is ours.
const forwardedProtoHeader = "X-Forwarded-Proto"

type setupStatusDTO struct {
	Required bool `json:"required"`
}

type newUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) error {
	required, err := a.d.Auth.SetupRequired(r.Context())
	if err != nil {
		return fmt.Errorf("checking setup: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, setupStatusDTO{Required: required})
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) error {
	var in newUserRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	u, err := a.d.Auth.Setup(r.Context(), auth.NewUser{Email: in.Email, Name: in.Name, Password: in.Password})
	if err != nil {
		return fmt.Errorf("setting up the owner: %w", err)
	}
	return a.writeJSON(w, r, http.StatusCreated, toUserDTO(u))
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionDTO struct {
	User      userDTO   `json:"user"`
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) error {
	var in loginRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	res, err := a.d.Auth.Login(r.Context(), in.Email, in.Password, a.clientAddr(r))
	if err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	http.SetCookie(w, a.sessionCookie(r, res.Token, res.ExpiresAt, 0))
	return a.writeJSON(w, r, http.StatusOK, sessionDTO{User: toUserDTO(res.User), CSRFToken: res.CSRFToken, ExpiresAt: res.ExpiresAt})
}

// logout ends the session behind the cookie, if any, and clears the cookie.
// It needs no CSRF token: forcing a logout is harmless and the call must work
// even when the page has lost its token.
func (a *API) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(a.d.Session.CookieName); err == nil && c.Value != "" {
		if err := a.d.Auth.Logout(r.Context(), c.Value); err != nil {
			return fmt.Errorf("logging out: %w", err)
		}
	}
	http.SetCookie(w, a.sessionCookie(r, "", time.Unix(0, 0), -1))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sessionCookie builds the session cookie from the configuration. maxAge < 0
// deletes it. The Secure attribute depends on the request (see secureCookie).
func (a *API) sessionCookie(r *http.Request, value string, expires time.Time, maxAge int) *http.Cookie {
	s := a.d.Session
	return &http.Cookie{ //nolint:gosec // G124: Secure and SameSite come from auth.session config; HttpOnly is always set
		Name: s.CookieName, Value: value, Path: s.CookiePath, Domain: s.CookieDomain,
		Expires: expires, MaxAge: maxAge, Secure: secureCookie(s.CookieSecure, a.d.Server.TrustForwardedFor, r),
		HttpOnly: true, SameSite: sameSite(s.CookieSameSite),
	}
}

// secureCookie decides the cookie's Secure attribute. always and never are
// fixed. auto follows the transport: a TLS connection, or, when the proxy in
// front is trusted (trustProxy, the server.trustForwardedFor setting), an
// X-Forwarded-Proto of https. Only the last value is used, the one the trusted
// proxy appended.
func secureCookie(mode config.CookieSecureMode, trustProxy bool, r *http.Request) bool {
	switch mode {
	case config.CookieSecureAlways:
		return true
	case config.CookieSecureNever:
		return false
	}
	if r.TLS != nil {
		return true
	}
	if !trustProxy {
		return false
	}
	values := strings.Split(r.Header.Get(forwardedProtoHeader), ",")
	return strings.EqualFold(strings.TrimSpace(values[len(values)-1]), "https")
}

func sameSite(v string) http.SameSite {
	switch v {
	case config.SameSiteStrict:
		return http.SameSiteStrictMode
	case config.SameSiteNone:
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

type meDTO struct {
	User userDTO `json:"user"`
	// AuthKind is "session" or "apiKey".
	AuthKind auth.Kind `json:"authKind"`
	// CSRFToken is set for session callers: send it in the CSRF header.
	CSRFToken string `json:"csrfToken,omitempty"`
	// Editor carries the server settings the editor needs at start.
	Editor editorSettingsDTO `json:"editor"`
}

// editorSettingsDTO is the editor part of GET /me.
type editorSettingsDTO struct {
	ChatBasePath     string `json:"chatBasePath"`
	MaxNoteTextBytes int    `json:"maxNoteTextBytes"`
}

func (a *API) me(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	editor := editorSettingsDTO{ChatBasePath: a.d.Editor.ChatBasePath, MaxNoteTextBytes: a.d.Editor.MaxNoteTextBytes}
	return a.writeJSON(w, r, http.StatusOK, meDTO{User: toUserDTO(p.User), AuthKind: p.Kind, CSRFToken: p.CSRFToken, Editor: editor})
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Auth.ListUsers(r.Context(), p, page)
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toUserDTO))
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	var in newUserRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	u, err := a.d.Auth.CreateMember(r.Context(), p, auth.NewUser{Email: in.Email, Name: in.Name, Password: in.Password})
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	w.Header().Set("Location", v1("/users/"+string(u.ID)))
	return a.writeJSON(w, r, http.StatusCreated, toUserDTO(u))
}

func userID(r *http.Request) (domain.UserID, error) {
	return idParam(r, "id", domain.UserID.Validate)
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := userID(r)
	if err != nil {
		return err
	}
	u, err := a.d.Auth.GetUser(r.Context(), p, id)
	if err != nil {
		return fmt.Errorf("getting user: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toUserDTO(u))
}

type userPatchRequest struct {
	Email           *string `json:"email"`
	Name            *string `json:"name"`
	Disabled        *bool   `json:"disabled"`
	Password        *string `json:"password"`
	CurrentPassword string  `json:"currentPassword"`
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := userID(r)
	if err != nil {
		return err
	}
	var in userPatchRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	u, err := a.d.Auth.UpdateUser(r.Context(), p, id, auth.UserPatch{
		Email: in.Email, Name: in.Name, Disabled: in.Disabled, Password: in.Password, CurrentPassword: in.CurrentPassword,
	})
	if err != nil {
		return fmt.Errorf("updating user: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, toUserDTO(u))
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := userID(r)
	if err != nil {
		return err
	}
	if err := a.d.Auth.DeleteUser(r.Context(), p, id); err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if err := allowParams(q, "limit", "cursor"); err != nil {
		return err
	}
	page, err := a.pageOf(q)
	if err != nil {
		return err
	}
	res, err := a.d.Auth.ListAPIKeys(r.Context(), p, page)
	if err != nil {
		return fmt.Errorf("listing API keys: %w", err)
	}
	return a.writeJSON(w, r, http.StatusOK, listOf(res, toAPIKeyDTO))
}

type newAPIKeyRequest struct {
	Name      string         `json:"name"`
	Scopes    []domain.Scope `json:"scopes"`
	ExpiresAt *time.Time     `json:"expiresAt"`
}

// createdAPIKeyDTO is the only response that ever carries the key's token.
type createdAPIKeyDTO struct {
	apiKeyDTO
	// Token is the full key. It is shown once and cannot be retrieved again.
	Token string `json:"token"`
}

func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	var in newAPIKeyRequest
	if err := decodeJSON(r, &in, false); err != nil {
		return err
	}
	res, err := a.d.Auth.CreateAPIKey(r.Context(), p, auth.NewAPIKey{Name: in.Name, Scopes: in.Scopes, ExpiresAt: in.ExpiresAt})
	if err != nil {
		return fmt.Errorf("creating API key: %w", err)
	}
	w.Header().Set("Location", v1("/api-keys/"+string(res.Key.ID)))
	w.Header().Set("Cache-Control", "no-store")
	return a.writeJSON(w, r, http.StatusCreated, createdAPIKeyDTO{apiKeyDTO: toAPIKeyDTO(res.Key), Token: res.Token})
}

func (a *API) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	p, err := mustPrincipal(r)
	if err != nil {
		return err
	}
	id, err := idParam(r, "id", domain.APIKeyID.Validate)
	if err != nil {
		return err
	}
	if err := a.d.Auth.RevokeAPIKey(r.Context(), p, id); err != nil {
		return fmt.Errorf("revoking API key: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
