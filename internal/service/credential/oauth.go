package credential

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// ErrInvalidState is returned by CompleteAuthorization for a state that is
// malformed, tampered with or expired.
var ErrInvalidState = errors.New("invalid or expired oauth2 state")

// OAuth2 grant_type values sent to token endpoints.
const (
	grantTypeClientCredentials = "client_credentials"
	grantTypeAuthCode          = "authorization_code"
	grantTypeRefresh           = "refresh_token"
)

// refreshLockPrefix namespaces the cluster-wide lock of one credential's token.
const refreshLockPrefix = "engi:oauth2:"

// stateAAD binds sealed OAuth2 state so it cannot be confused with a credential.
const stateAAD = "engi.oauth2.state"

// verifierBytes is the entropy of a PKCE code verifier (RFC 7636 asks for 32+).
const verifierBytes = 32

// token is the token set stored, encrypted, inside the credential data.
type token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitzero"`
	NeedsReauth  bool      `json:"needsReauth,omitempty"`
}

// fresh reports whether the access token is usable beyond the skew.
func (t token) fresh(now time.Time, skew time.Duration) bool {
	if t.AccessToken == "" || t.NeedsReauth {
		return false
	}
	return t.ExpiresAt.IsZero() || now.Add(skew).Before(t.ExpiresAt)
}

func parseToken(v any) token {
	var t token
	if v == nil {
		return t
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return t
	}
	_ = json.Unmarshal(raw, &t) // a malformed token is treated as absent
	return t
}

func str(data map[string]any, field string) string {
	if field == "" || data[field] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(data[field]))
}

func (s *Service) tokenRequest(spec *nodesdk.OAuth2Spec, data map[string]any, grant string) port.OAuth2TokenRequest {
	return port.OAuth2TokenRequest{
		TokenURL: str(data, spec.TokenURLField), GrantType: grant,
		ClientID: str(data, spec.ClientIDField), ClientSecret: str(data, spec.ClientSecretField),
		SendCredentialsInBody: str(data, spec.AuthStyleField) == "body",
		Scopes:                strings.Fields(str(data, spec.ScopeField)),
	}
}

func (s *Service) toToken(res port.OAuth2Token, prev token) token {
	t := token{AccessToken: res.AccessToken, RefreshToken: res.RefreshToken, TokenType: res.TokenType, Scope: res.Scope}
	if t.RefreshToken == "" {
		t.RefreshToken = prev.RefreshToken // providers may omit it on refresh
	}
	if res.ExpiresIn > 0 {
		t.ExpiresAt = s.d.Clock.Now().Add(res.ExpiresIn)
	}
	return t
}

// withAccess returns data with the managed token removed and the access token
// exposed under the spec's AccessField, for handing to nodes.
func withAccess(spec *nodesdk.OAuth2Spec, data map[string]any, t token) map[string]any {
	out := make(map[string]any, len(data)+1)
	for k, v := range data {
		if k != spec.TokenField {
			out[k] = v
		}
	}
	out[spec.AccessField] = t.AccessToken
	return out
}

// oauthData returns the credential data with a usable access token, fetching
// or refreshing it under two locks so that one token request runs per
// credential across the cluster. The in-process lock coalesces callers of
// this process; the RefreshLock section (when configured) serialises replicas.
// Inside each section the credential is re-read, so a caller that waited
// reuses a token another caller just stored instead of refreshing again.
// force skips the freshness checks (used by Test).
func (s *Service) oauthData(ctx context.Context, row domain.Credential, typ nodesdk.CredentialType, data map[string]any, force bool) (map[string]any, error) {
	spec := typ.OAuth2
	if t := parseToken(data[spec.TokenField]); !force && t.fresh(s.d.Clock.Now(), s.cfg.OAuth2RefreshSkew) {
		return withAccess(spec, data, t), nil
	}
	if s.d.OAuth2 == nil {
		return nil, errors.New("oauth2 is not configured")
	}
	unlock, err := s.locks.lock(ctx, string(row.ID))
	if err != nil {
		return nil, err
	}
	defer unlock()

	var out map[string]any
	var refused error // a permanent refusal: stored, and reported after commit
	section := func(ctx context.Context) error {
		cur := data
		if !force {
			// Another caller, here or on another replica, may have refreshed
			// while we waited for the lock: re-read.
			fresh, err := s.d.Store.Get(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("reloading credential: %w", err)
			}
			if cur, err = s.open(ctx, fresh); err != nil {
				return err
			}
			if t := parseToken(cur[spec.TokenField]); t.fresh(s.d.Clock.Now(), s.cfg.OAuth2RefreshSkew) {
				out = withAccess(spec, cur, t)
				return nil
			}
			row = fresh
		}
		t, err := s.fetchToken(ctx, row, spec, cur)
		if errors.Is(err, port.ErrOAuth2InvalidGrant) {
			prev := parseToken(cur[spec.TokenField])
			prev.NeedsReauth = true
			if err := s.storeToken(ctx, row, spec, cur, prev); err != nil {
				return err
			}
			s.d.Logger.WarnContext(ctx, "oauth2 credential needs re-authorization", "credential", row.ID)
			// Returning nil keeps the NeedsReauth mark: an error would roll it back.
			refused = fmt.Errorf("credential %s: %w: %w", row.ID, domain.ErrNeedsReauth, err)
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.storeToken(ctx, row, spec, cur, t); err != nil {
			return err
		}
		s.d.Logger.DebugContext(ctx, "oauth2 token stored", "credential", row.ID)
		out = withAccess(spec, cur, t)
		return nil
	}
	if err := s.withRefreshLock(ctx, row.ID, section); err != nil {
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}
	return out, nil
}

// withRefreshLock runs fn under the cluster-wide refresh lock of the
// credential. Without a RefreshLock the process is the only writer, and the
// in-process lock held by the caller is enough.
func (s *Service) withRefreshLock(ctx context.Context, id domain.CredentialID, fn func(ctx context.Context) error) error {
	if s.d.RefreshLock == nil {
		return fn(ctx)
	}
	if err := s.d.RefreshLock.WithLock(ctx, refreshLockPrefix+string(id), fn); err != nil {
		return fmt.Errorf("refreshing credential %s: %w", id, err)
	}
	return nil
}

// fetchToken asks the token endpoint for a new token set. It stores nothing.
// A missing refresh token on the authorization-code grant needs re-authorization.
func (s *Service) fetchToken(ctx context.Context, row domain.Credential, spec *nodesdk.OAuth2Spec, data map[string]any) (token, error) {
	prev := parseToken(data[spec.TokenField])
	var res port.OAuth2Token
	var err error
	switch nodesdk.OAuth2Grant(str(data, spec.GrantField)) {
	case nodesdk.GrantClientCredentials:
		res, err = s.d.OAuth2.Token(ctx, s.tokenRequest(spec, data, grantTypeClientCredentials))
	case nodesdk.GrantAuthorizationCode:
		if prev.RefreshToken == "" || prev.NeedsReauth {
			return token{}, fmt.Errorf("credential %s: %w", row.ID, domain.ErrNeedsReauth)
		}
		req := s.tokenRequest(spec, data, grantTypeRefresh)
		req.RefreshToken = prev.RefreshToken
		res, err = s.d.OAuth2.Token(ctx, req)
	default:
		return token{}, fmt.Errorf("credential %s: unknown oauth2 grant type", row.ID)
	}
	if err != nil {
		// Keep ErrOAuth2InvalidGrant matchable by the caller.
		return token{}, fmt.Errorf("fetching oauth2 token for credential %s: %w", row.ID, err)
	}
	return s.toToken(res, prev), nil
}

// storeToken writes the token set back, encrypted. The caller holds the lock.
func (s *Service) storeToken(ctx context.Context, row domain.Credential, spec *nodesdk.OAuth2Spec, data map[string]any, t token) error {
	raw, err := json.Marshal(t) //nolint:gosec // the token set is encrypted before it is stored
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}
	next := make(map[string]any, len(data)+1)
	for k, v := range data {
		next[k] = v
	}
	next[spec.TokenField] = asMap
	keyID, ct, err := s.seal(ctx, row.ID, next)
	if err != nil {
		return err
	}
	row.KeyID, row.Data = keyID, ct
	if err := s.d.Store.Update(ctx, row); err != nil {
		return fmt.Errorf("storing oauth2 token: %w", err)
	}
	return nil
}

// pkceState is what BeginAuthorization seals into the OAuth2 state parameter.
type pkceState struct {
	Credential domain.CredentialID `json:"c"`
	Verifier   string              `json:"v"`
	Expires    int64               `json:"e"`
}

// BeginAuthorization starts the authorization-code flow with PKCE (S256) and
// returns the provider URL to send the user to. State and verifier travel in
// the state parameter, sealed with the credential cipher, so no server-side
// storage is needed and any replica can complete the flow.
func (s *Service) BeginAuthorization(ctx context.Context, id domain.CredentialID) (string, error) {
	if s.cfg.OAuth2RedirectURL == "" {
		return "", errors.New("credentials.oauth2RedirectUrl is not configured")
	}
	row, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("loading credential: %w", err)
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return "", err
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return "", err
	}
	spec := typ.OAuth2
	if spec == nil || nodesdk.OAuth2Grant(str(data, spec.GrantField)) != nodesdk.GrantAuthorizationCode {
		return "", &domain.ValidationError{Issues: []domain.Issue{{Path: "type", Message: "credential does not use the authorization code grant"}}}
	}
	vb := make([]byte, verifierBytes)
	if _, err := io.ReadFull(s.d.Random, vb); err != nil {
		return "", fmt.Errorf("generating pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(vb)
	state, err := s.sealState(ctx, pkceState{Credential: id, Verifier: verifier, Expires: s.d.Clock.Now().Add(s.cfg.OAuth2StateTTL).Unix()})
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	u, err := url.Parse(str(data, spec.AuthURLField))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", &domain.ValidationError{Issues: []domain.Issue{{Path: "data." + spec.AuthURLField, Message: "must be an absolute URL"}}}
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", str(data, spec.ClientIDField))
	q.Set("redirect_uri", s.cfg.OAuth2RedirectURL)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	if scope := str(data, spec.ScopeField); scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CompleteAuthorization finishes the flow: it validates state, exchanges code
// for tokens and stores them encrypted in the credential. It returns the
// credential ID so the caller can redirect back to it.
func (s *Service) CompleteAuthorization(ctx context.Context, state, code string) (domain.CredentialID, error) {
	if code == "" {
		return "", fmt.Errorf("authorization code is empty: %w", ErrInvalidState)
	}
	st, err := s.openState(ctx, state)
	if err != nil {
		return "", err
	}
	if s.d.OAuth2 == nil {
		return "", errors.New("oauth2 is not configured")
	}
	unlock, err := s.locks.lock(ctx, string(st.Credential))
	if err != nil {
		return "", err
	}
	defer unlock()
	row, err := s.d.Store.Get(ctx, st.Credential)
	if err != nil {
		return "", fmt.Errorf("loading credential: %w", err)
	}
	typ, err := s.typeOf(row.Type)
	if err != nil {
		return "", err
	}
	data, err := s.open(ctx, row)
	if err != nil {
		return "", err
	}
	spec := typ.OAuth2
	if spec == nil {
		return "", fmt.Errorf("credential %s is not an oauth2 credential: %w", row.ID, ErrInvalidState)
	}
	req := s.tokenRequest(spec, data, grantTypeAuthCode)
	req.Code, req.RedirectURI, req.CodeVerifier = code, s.cfg.OAuth2RedirectURL, st.Verifier
	req.Scopes = nil // scopes belong to the authorization request
	res, err := s.d.OAuth2.Token(ctx, req)
	if err != nil {
		return "", fmt.Errorf("exchanging authorization code for credential %s: %w", row.ID, err)
	}
	if err := s.storeToken(ctx, row, spec, data, s.toToken(res, token{})); err != nil {
		return "", err
	}
	s.d.Logger.InfoContext(ctx, "oauth2 credential connected", "credential", row.ID)
	return row.ID, nil
}

func (s *Service) sealState(ctx context.Context, st pkceState) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", fmt.Errorf("encoding state: %w", err)
	}
	keyID, ct, err := s.d.Cipher.Encrypt(ctx, raw, []byte(stateAAD))
	if err != nil {
		return "", fmt.Errorf("sealing state: %w", err)
	}
	return keyID + "." + base64.RawURLEncoding.EncodeToString(ct), nil
}

func (s *Service) openState(ctx context.Context, state string) (pkceState, error) {
	keyID, enc, ok := strings.Cut(state, ".")
	if !ok {
		return pkceState{}, ErrInvalidState
	}
	ct, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return pkceState{}, ErrInvalidState
	}
	raw, err := s.d.Cipher.Decrypt(ctx, keyID, ct, []byte(stateAAD))
	if err != nil {
		return pkceState{}, ErrInvalidState
	}
	var st pkceState
	if err := json.Unmarshal(raw, &st); err != nil || s.d.Clock.Now().Unix() > st.Expires {
		return pkceState{}, ErrInvalidState
	}
	return st, nil
}
