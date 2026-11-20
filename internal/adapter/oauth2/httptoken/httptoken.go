// Package httptoken implements port.OAuth2Client over HTTP.
package httptoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// maxResponseBytes bounds how much of a token response is read.
const maxResponseBytes = 1 << 20

// Client calls OAuth2 token endpoints.
type Client struct {
	http    nodesdk.HTTPClient
	timeout time.Duration
}

var _ port.OAuth2Client = (*Client)(nil)

// New returns a Client using the shared HTTP client; timeout bounds each call.
func New(httpc nodesdk.HTTPClient, timeout time.Duration) *Client {
	return &Client{http: httpc, timeout: timeout}
}

// Token implements port.OAuth2Client.
func (c *Client) Token(ctx context.Context, tr port.OAuth2TokenRequest) (port.OAuth2Token, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	form := url.Values{"grant_type": {tr.GrantType}}
	if len(tr.Scopes) > 0 {
		form.Set("scope", strings.Join(tr.Scopes, " "))
	}
	setIf := func(k, v string) {
		if v != "" {
			form.Set(k, v)
		}
	}
	setIf("code", tr.Code)
	setIf("redirect_uri", tr.RedirectURI)
	setIf("code_verifier", tr.CodeVerifier)
	setIf("refresh_token", tr.RefreshToken)
	if tr.SendCredentialsInBody {
		setIf("client_id", tr.ClientID)
		setIf("client_secret", tr.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tr.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return port.OAuth2Token{}, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if !tr.SendCredentialsInBody && tr.ClientID != "" {
		req.SetBasicAuth(url.QueryEscape(tr.ClientID), url.QueryEscape(tr.ClientSecret))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return port.OAuth2Token{}, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return port.OAuth2Token{}, fmt.Errorf("reading token response: %w", err)
	}
	return parse(resp.StatusCode, body)
}

type tokenBody struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
	Scope        string          `json:"scope"`
	Error        string          `json:"error"`
	Description  string          `json:"error_description"`
}

func parse(status int, body []byte) (port.OAuth2Token, error) {
	var tb tokenBody
	jsonErr := json.Unmarshal(body, &tb)
	if status < 200 || status > 299 || tb.Error != "" {
		// Never echo the body: it may contain tokens or client details.
		msg := fmt.Sprintf("token endpoint returned status %d", status)
		if tb.Error != "" {
			msg += ": " + tb.Error
			if tb.Description != "" {
				msg += " (" + tb.Description + ")"
			}
		}
		if tb.Error == "invalid_grant" || tb.Error == "invalid_token" {
			return port.OAuth2Token{}, fmt.Errorf("%s: %w", msg, port.ErrOAuth2InvalidGrant)
		}
		return port.OAuth2Token{}, errors.New(msg)
	}
	if jsonErr != nil {
		return port.OAuth2Token{}, errors.New("token endpoint returned a response that is not JSON")
	}
	if tb.AccessToken == "" {
		return port.OAuth2Token{}, errors.New("token endpoint response has no access_token")
	}
	tok := port.OAuth2Token{AccessToken: tb.AccessToken, RefreshToken: tb.RefreshToken, TokenType: tb.TokenType, Scope: tb.Scope}
	if len(tb.ExpiresIn) > 0 {
		secs, err := strconv.ParseFloat(strings.Trim(string(tb.ExpiresIn), `"`), 64)
		if err == nil && secs > 0 {
			tok.ExpiresIn = time.Duration(secs * float64(time.Second))
		}
	}
	return tok, nil
}
