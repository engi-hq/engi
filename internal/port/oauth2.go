package port

import (
	"context"
	"errors"
	"time"
)

// OAuth2TokenRequest is one call to an OAuth2 token endpoint.
type OAuth2TokenRequest struct {
	TokenURL string
	// GrantType is client_credentials, authorization_code or refresh_token.
	GrantType    string
	ClientID     string
	ClientSecret string
	// SendCredentialsInBody sends the client credentials as form fields
	// instead of an HTTP Basic header.
	SendCredentialsInBody bool
	Scopes                []string
	Code                  string
	RedirectURI           string
	CodeVerifier          string
	RefreshToken          string
}

// OAuth2Token is a token endpoint response.
type OAuth2Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	// ExpiresIn is zero when the provider sent no lifetime.
	ExpiresIn time.Duration
	Scope     string
}

// OAuth2Client talks to OAuth2 token endpoints. A rejected grant yields an
// error wrapping ErrOAuth2InvalidGrant.
type OAuth2Client interface {
	Token(ctx context.Context, req OAuth2TokenRequest) (OAuth2Token, error)
}

// ErrOAuth2InvalidGrant marks a token endpoint rejecting a code or refresh
// token (HTTP 400/401 with an OAuth2 error body); the user must reconnect.
var ErrOAuth2InvalidGrant = errors.New("oauth2 grant rejected")
