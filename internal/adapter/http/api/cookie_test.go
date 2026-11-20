package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
)

func TestSecureCookieModes(t *testing.T) {
	tests := []struct {
		name       string
		mode       config.CookieSecureMode
		tls        bool
		trustProxy bool
		fwdProto   string
		want       bool
	}{
		{"always over plain http", config.CookieSecureAlways, false, false, "", true},
		{"always over tls", config.CookieSecureAlways, true, false, "", true},
		{"never over tls", config.CookieSecureNever, true, true, "https", false},
		{"never over plain http", config.CookieSecureNever, false, false, "", false},
		{"auto over tls", config.CookieSecureAuto, true, false, "", true},
		{"auto plain http, no proxy", config.CookieSecureAuto, false, false, "", false},
		{"auto plain http, trusted proxy says https", config.CookieSecureAuto, false, true, "https", true},
		{"auto plain http, trusted proxy says http", config.CookieSecureAuto, false, true, "http", false},
		{"auto, proxy header ignored when not trusted", config.CookieSecureAuto, false, false, "https", false},
		{"auto, last forwarded proto wins", config.CookieSecureAuto, false, true, "http, HTTPS", true},
		{"auto, client-supplied first entry ignored", config.CookieSecureAuto, false, true, "https, http", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", http.NoBody)
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.fwdProto != "" {
				r.Header.Set(forwardedProtoHeader, tt.fwdProto)
			}
			a := &API{d: Deps{
				Session: config.SessionConfig{CookieName: "engi_session", CookiePath: "/", CookieSecure: tt.mode, CookieSameSite: config.SameSiteLax},
				Server:  config.ServerConfig{TrustForwardedFor: tt.trustProxy},
			}}
			c := a.sessionCookie(r, "token", time.Now().Add(time.Hour), 0)
			if c.Secure != tt.want {
				t.Fatalf("Secure = %v, want %v", c.Secure, tt.want)
			}
			if !c.HttpOnly {
				t.Fatal("session cookie must be HttpOnly")
			}
		})
	}
}
