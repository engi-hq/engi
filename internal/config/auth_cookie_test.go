package config

import (
	"strings"
	"testing"
)

func TestCookieSecureModeFromEnvAndFile(t *testing.T) {
	tests := []struct {
		name string
		env  string
		file string
		want CookieSecureMode
	}{
		{"default is always", "", "", CookieSecureAlways},
		{"auto from env", "ENGI_AUTH_SESSION_COOKIE_SECURE=auto", "", CookieSecureAuto},
		{"always from env", "ENGI_AUTH_SESSION_COOKIE_SECURE=always", "", CookieSecureAlways},
		{"never from env", "ENGI_AUTH_SESSION_COOKIE_SECURE=never", "", CookieSecureNever},
		{"mixed case and spaces", "ENGI_AUTH_SESSION_COOKIE_SECURE= Never ", "", CookieSecureNever},
		{"legacy false means never", "ENGI_AUTH_SESSION_COOKIE_SECURE=false", "", CookieSecureNever},
		{"legacy true means always", "ENGI_AUTH_SESSION_COOKIE_SECURE=true", "", CookieSecureAlways},
		{"auto from yaml", "", "auth:\n  session:\n    cookieSecure: auto\n", CookieSecureAuto},
		{"legacy yaml false", "", "auth:\n  session:\n    cookieSecure: false\n", CookieSecureNever},
		{"legacy yaml true", "", "auth:\n  session:\n    cookieSecure: true\n", CookieSecureAlways},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := LoadOptions{ReadFile: fileReader(tt.file)}
			if tt.file != "" {
				opts.Args = []string{"--config=x"}
			}
			if tt.env != "" {
				opts.Environ = []string{tt.env}
			}
			cfg, err := Load(opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Auth.Session.CookieSecure; got != tt.want {
				t.Fatalf("cookieSecure = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCookieSecureModeRejectsUnknownValues(t *testing.T) {
	tests := []struct {
		name string
		opts LoadOptions
	}{
		{"env", LoadOptions{Environ: []string{"ENGI_AUTH_SESSION_COOKIE_SECURE=sometimes"}}},
		{"flag", LoadOptions{Args: []string{"--auth-session-cookie-secure=sometimes"}}},
		{"yaml", LoadOptions{Args: []string{"--config=x"}, ReadFile: fileReader("auth:\n  session:\n    cookieSecure: sometimes\n")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.opts)
			if err == nil || !strings.Contains(err.Error(), "cookieSecure") {
				t.Fatalf("error = %v, want one naming auth.session.cookieSecure", err)
			}
		})
	}
}

func TestCookieSecureValidate(t *testing.T) {
	tests := []struct {
		name     string
		mode     CookieSecureMode
		sameSite string
		wantErr  string
	}{
		{"auto lax", CookieSecureAuto, SameSiteLax, ""},
		{"always none", CookieSecureAlways, SameSiteNone, ""},
		{"auto none", CookieSecureAuto, SameSiteNone, ""},
		{"never lax", CookieSecureNever, SameSiteLax, ""},
		{"never none", CookieSecureNever, SameSiteNone, "requires auth.session.cookieSecure auto or always"},
		{"unknown mode", CookieSecureMode("maybe"), SameSiteLax, `"maybe" is not one of auto, always, never`},
		{"empty mode", "", SameSiteLax, "is not one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Auth.Session.CookieSecure = tt.mode
			cfg.Auth.Session.CookieSameSite = tt.sameSite
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
