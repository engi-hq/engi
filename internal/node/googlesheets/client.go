package googlesheets

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/credtype/googleserviceaccount"
	"github.com/engi-hq/engi/internal/node/chatkit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const (
	// sheetsScope is the OAuth2 scope a service-account token is requested for.
	sheetsScope = "https://www.googleapis.com/auth/spreadsheets"
	// tokenLifetime is how long a minted service-account token is valid (Google's maximum).
	tokenLifetime = time.Hour
	jwtGrantType  = "urn:ietf:params:oauth:grant-type:jwt-bearer"
)

// ErrSheets is wrapped by every error the Sheets API reports.
var ErrSheets = errors.New("google sheets API error")

type client struct {
	cfg     Config
	http    nodesdk.HTTPClient
	auth    func(*http.Request) error
	secrets []string
}

// call sends one Sheets API request. path is relative to the base URL; the
// caller escapes path segments. A non-2xx answer becomes a *nodesdk.NodeError
// with the HTTP status and Google's message.
func (c *client) call(ctx context.Context, method, path string, query url.Values, body any) (map[string]any, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		payload = b
	}
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		u := strings.TrimRight(c.cfg.BaseURL, "/") + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if err := c.auth(req); err != nil {
			return nil, fmt.Errorf("applying credential: %w", err)
		}
		return req, nil
	}, c.secrets...)
	if err != nil {
		return nil, fmt.Errorf("google sheets %s: %w", method, err)
	}
	var out map[string]any
	if len(bytes.TrimSpace(resp.Body)) > 0 {
		if err := json.Unmarshal(resp.Body, &out); err != nil && resp.Status/100 == 2 {
			return nil, &nodesdk.NodeError{Message: fmt.Sprintf("google sheets: unexpected response (HTTP %d)", resp.Status), HTTPStatus: resp.Status}
		}
	}
	if resp.Status/100 != 2 {
		return nil, &nodesdk.NodeError{Message: "google sheets: " + apiMessage(out, resp.Status, c.secrets), HTTPStatus: resp.Status, Err: ErrSheets}
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// apiMessage extracts Google's error text from {"error":{"message","status"}}.
func apiMessage(body map[string]any, status int, secrets []string) string {
	e, _ := body["error"].(map[string]any)
	msg, _ := e["message"].(string)
	st, _ := e["status"].(string)
	if msg == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	if st != "" {
		msg = st + ": " + msg
	}
	return chatkit.Redact(msg, secrets...)
}

// serviceAccountToken signs a JWT with the key and exchanges it for an access
// token at the credential's token URL. The token lives for one node run.
func (c *client) serviceAccountToken(ctx context.Context, cred nodesdk.Credential) (string, error) {
	email := chatkit.Secret(cred, googleserviceaccount.FieldEmail)
	keyPEM := chatkit.Secret(cred, googleserviceaccount.FieldPrivateKey)
	tokenURL := chatkit.Secret(cred, googleserviceaccount.FieldTokenURL)
	if tokenURL == "" {
		tokenURL = googleserviceaccount.DefaultTokenURL
	}
	if email == "" || keyPEM == "" {
		return "", errors.New("service account credential needs an email and a private key")
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return "", err
	}
	now := c.cfg.Now()
	claims := map[string]any{"iss": email, "scope": sheetsScope, "aud": tokenURL, "iat": now.Unix(), "exp": now.Add(tokenLifetime).Unix()}
	if sub := chatkit.Secret(cred, googleserviceaccount.FieldSubject); sub != "" {
		claims["sub"] = sub
	}
	assertion, err := signJWT(key, claims)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {jwtGrantType}, "assertion": {assertion}}.Encode()
	resp, err := c.cfg.Caller.Do(ctx, c.http, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form))
		if err != nil {
			return nil, fmt.Errorf("creating token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	}, assertion, keyPEM)
	if err != nil {
		return "", fmt.Errorf("requesting service account token: %w", err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(resp.Body, &tok)
	if resp.Status/100 != 2 || tok.AccessToken == "" {
		msg := strings.TrimSpace(tok.Error + " " + tok.Description)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.Status)
		}
		return "", &nodesdk.NodeError{Message: "google token endpoint: " + chatkit.Redact(msg, assertion), HTTPStatus: resp.Status, Err: ErrSheets}
	}
	return tok.AccessToken, nil
}

func parseKey(keyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("service account private key is not PEM encoded")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("service account private key is not an RSA key")
		}
		return rk, nil
	}
	rk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("service account private key could not be parsed")
	}
	return rk, nil
}

// signJWT returns a compact RS256 JWT.
func signJWT(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	enc := base64.RawURLEncoding
	head, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("encoding jwt header: %w", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encoding jwt claims: %w", err)
	}
	signing := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing jwt: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
