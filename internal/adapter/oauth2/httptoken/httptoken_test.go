package httptoken_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/oauth2/httptoken"
	"github.com/engi-hq/engi/internal/port"
)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestToken(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    port.OAuth2Token
		wantErr string
		invalid bool
	}{
		{"numeric expiry", 200, `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":60}`,
			port.OAuth2Token{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", ExpiresIn: time.Minute}, "", false},
		{"string expiry", 200, `{"access_token":"a","expires_in":"30"}`, port.OAuth2Token{AccessToken: "a", ExpiresIn: 30 * time.Second}, "", false},
		{"no expiry", 200, `{"access_token":"a"}`, port.OAuth2Token{AccessToken: "a"}, "", false},
		{"invalid grant", 400, `{"error":"invalid_grant","error_description":"expired"}`, port.OAuth2Token{}, "invalid_grant", true},
		{"other error", 401, `{"error":"invalid_client"}`, port.OAuth2Token{}, "invalid_client", false},
		{"not json", 200, `<html>secret-page</html>`, port.OAuth2Token{}, "not JSON", false},
		{"missing token", 200, `{}`, port.OAuth2Token{}, "no access_token", false},
		{"server error body not echoed", 500, `secret-internal-details`, port.OAuth2Token{}, "status 500", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := httptoken.New(http.DefaultClient, time.Second)
			got, err := c.Token(context.Background(), port.OAuth2TokenRequest{TokenURL: serve(t, tt.status, tt.body).URL, GrantType: "client_credentials"})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, port.ErrOAuth2InvalidGrant) != tt.invalid {
					t.Fatalf("err = %v", err)
				}
				if strings.Contains(err.Error(), "secret-") {
					t.Fatalf("error echoes the response body: %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestTokenTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	c := httptoken.New(http.DefaultClient, 50*time.Millisecond)
	if _, err := c.Token(context.Background(), port.OAuth2TokenRequest{TokenURL: srv.URL}); err == nil {
		t.Fatal("expected timeout")
	}
}
