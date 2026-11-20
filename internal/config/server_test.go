package config

import (
	"strings"
	"testing"
)

func TestServerAndAuthDefaultsAndFlags(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Args:    []string{"--roles", "api,worker", "--server-addr", ":9090"},
		Environ: []string{"ENGI_SERVER_AUTO_MIGRATE=false", "ENGI_AUTH_SESSION_COOKIE_SECURE=false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":9090" || cfg.Server.AutoMigrate || cfg.Auth.Session.CookieSecure != CookieSecureNever {
		t.Fatalf("server = %+v", cfg.Server)
	}
	if !cfg.Server.HasRole(RoleAPI) || cfg.Server.HasRole(RoleWebhook) {
		t.Fatalf("roles = %v", cfg.Server.Roles)
	}
}

func TestServerValidation(t *testing.T) {
	cfg := Default()
	cfg.Server.Roles = []string{"bogus"}
	cfg.Server.CORS.AllowedOrigins = []string{"*"}
	cfg.Server.CORS.AllowCredentials = true
	cfg.Server.Pagination.MaxLimit = 1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"unknown role", "wildcard", "maxLimit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestUIConfig(t *testing.T) {
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Server.UI.Enabled || cfg.Server.UI.ContentSecurityPolicy != "" {
		t.Fatalf("defaults = %+v", cfg.Server.UI)
	}
	cfg, err = Load(LoadOptions{Environ: []string{"ENGI_SERVER_UI_ENABLED=false", "ENGI_SERVER_UI_CONTENT_SECURITY_POLICY=default-src 'none'"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.UI.Enabled || cfg.Server.UI.ContentSecurityPolicy != "default-src 'none'" {
		t.Fatalf("overrides = %+v", cfg.Server.UI)
	}
}

func TestPublicURL(t *testing.T) {
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PublicURL != "http://localhost:8080" {
		t.Fatalf("default publicUrl = %q", cfg.Server.PublicURL)
	}
	cfg, err = Load(LoadOptions{Environ: []string{"ENGI_SERVER_PUBLIC_URL=https://engi.example.com/base"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.PublicURL != "https://engi.example.com/base" {
		t.Fatalf("env publicUrl = %q", cfg.Server.PublicURL)
	}
}

func TestValidatePublicURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"http with port", "http://localhost:8080", ""},
		{"https with path", "https://engi.example.com/engi", ""},
		{"empty", "", "must start with http"},
		{"relative", "/hook", "must start with http"},
		{"other scheme", "ftp://engi.example.com", "must start with http"},
		{"no host", "https:///x", "has no host"},
		{"query", "https://engi.example.com?x=1", "query or fragment"},
		{"fragment", "https://engi.example.com#x", "query or fragment"},
		{"trailing slash", "https://engi.example.com/", "must not end with /"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePublicURL(tt.url)
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

func TestPublicURLValidationReportsSetting(t *testing.T) {
	cfg := Default()
	cfg.Server.PublicURL = "engi.example.com"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "server.publicUrl") {
		t.Fatalf("error = %v", err)
	}
}
