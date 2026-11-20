package config

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestCredentialKeyringFromEnvAndFile(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Environ: []string{"ENGI_ENCRYPTION_KEYS=a:QUJD,b:REVG", "ENGI_ENCRYPTION_ACTIVE_KEY_ID=b", "ENGI_ENCRYPTION_KEYS_FILE=/keys"},
		ReadFile: func(p string) ([]byte, error) {
			if p != "/keys" {
				t.Fatalf("path = %s", p)
			}
			return []byte("# comment\nc:R0hJ\n"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Credentials.Keys) != 3 || cfg.Credentials.Keys["c"].Reveal() != "R0hJ" || cfg.Credentials.ActiveKeyID != "b" {
		t.Fatalf("keys = %v active=%s", cfg.Credentials.Keys, cfg.Credentials.ActiveKeyID)
	}
	if err := cfg.Credentials.RequireKeyring(); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialKeyringValidation(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    string
	}{
		{"keys without active id", []string{"ENGI_ENCRYPTION_KEYS=a:QUJD"}, "activeKeyId must be set"},
		{"active id unknown", []string{"ENGI_ENCRYPTION_KEYS=a:QUJD", "ENGI_ENCRYPTION_ACTIVE_KEY_ID=z"}, "no matching key"},
		{"malformed entry", []string{"ENGI_ENCRYPTION_KEYS=nocolon"}, "keyId:base64key"},
		{"bad batch size", []string{"ENGI_CREDENTIALS_ROTATION_BATCH_SIZE=0"}, "rotationBatchSize"},
		{"bad refresh lock timeout", []string{"ENGI_CREDENTIALS_OAUTH2_REFRESH_LOCK_TIMEOUT=0s"}, "oauth2RefreshLockTimeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(LoadOptions{Environ: tt.environ})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNoKeyringIsValidButNotForServers(t *testing.T) {
	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Credentials.Enabled() {
		t.Fatal("keyring must not be enabled by default")
	}
	err = cfg.Credentials.RequireKeyring()
	if err == nil || !strings.Contains(err.Error(), "ENGI_ENCRYPTION_KEYS") || !strings.Contains(err.Error(), "openssl rand -base64 32") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret("topsecret")
	for _, out := range []string{fmt.Sprintf("%v %+v %#v %s", s, s, s, s), slog.AnyValue(s).Resolve().String()} {
		if strings.Contains(out, "topsecret") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	cfg := Default()
	cfg.Credentials.Keys = map[string]Secret{"k": "topsecret"}
	if strings.Contains(fmt.Sprintf("%+v", cfg.Credentials), "topsecret") {
		t.Fatal("secret leaked through config printing")
	}
}
