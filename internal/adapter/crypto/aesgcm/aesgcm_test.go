package aesgcm_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/crypto/aesgcm"
	"github.com/engi-hq/engi/internal/config"
)

func key(t *testing.T) config.Secret {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return config.Secret(base64.StdEncoding.EncodeToString(b))
}

func cfg(t *testing.T, active string, ids ...string) config.CredentialsConfig {
	c := config.Default().Credentials
	c.ActiveKeyID = active
	c.Keys = map[string]config.Secret{}
	for _, id := range ids {
		c.Keys[id] = key(t)
	}
	return c
}

func TestRoundTripAndNonce(t *testing.T) {
	ctx := context.Background()
	c, err := aesgcm.New(cfg(t, "k1", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	id, ct1, err := c.Encrypt(ctx, []byte("secret"), []byte("cr_1"))
	if err != nil || id != "k1" {
		t.Fatalf("encrypt: %q %v", id, err)
	}
	_, ct2, _ := c.Encrypt(ctx, []byte("secret"), []byte("cr_1"))
	if bytes.Equal(ct1, ct2) {
		t.Fatal("same plaintext produced identical ciphertext (nonce reuse)")
	}
	if bytes.Contains(ct1, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := c.Decrypt(ctx, id, ct1, []byte("cr_1"))
	if err != nil || string(got) != "secret" {
		t.Fatalf("decrypt: %q %v", got, err)
	}
}

func TestDecryptFailures(t *testing.T) {
	ctx := context.Background()
	c, _ := aesgcm.New(cfg(t, "k1", "k1", "k2"))
	id, ct, _ := c.Encrypt(ctx, []byte("secret"), []byte("cr_1"))
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 1
	tests := []struct {
		name  string
		keyID string
		ct    []byte
		aad   string
		want  error
	}{
		{"wrong aad", id, ct, "cr_2", aesgcm.ErrDecrypt},
		{"wrong key", "k2", ct, "cr_1", aesgcm.ErrDecrypt},
		{"unknown key", "k9", ct, "cr_1", aesgcm.ErrUnknownKey},
		{"tampered", id, tampered, "cr_1", aesgcm.ErrDecrypt},
		{"truncated", id, ct[:5], "cr_1", aesgcm.ErrDecrypt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Decrypt(ctx, tt.keyID, tt.ct, []byte(tt.aad)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestKeyringErrors(t *testing.T) {
	bad := cfg(t, "k1", "k1")
	bad.Keys["k1"] = config.Secret(base64.StdEncoding.EncodeToString([]byte("short")))
	short := cfg(t, "k1", "k1")
	short.Keys["k1"] = "not base64 !!"
	tests := []struct {
		name string
		cfg  config.CredentialsConfig
		want string
	}{
		{"no keys", config.Default().Credentials, "no credential encryption key"},
		{"wrong size", bad, "must be 32 bytes"},
		{"not base64", short, "not valid base64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := aesgcm.New(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestOldKeyStillDecrypts(t *testing.T) {
	ctx := context.Background()
	base := cfg(t, "k1", "k1")
	old, _ := aesgcm.New(base)
	id, ct, _ := old.Encrypt(ctx, []byte("x"), nil)
	base.Keys["k2"] = key(t)
	base.ActiveKeyID = "k2"
	rotated, _ := aesgcm.New(base)
	if got, err := rotated.Decrypt(ctx, id, ct, nil); err != nil || string(got) != "x" {
		t.Fatalf("old key: %q %v", got, err)
	}
	if newID, _, _ := rotated.Encrypt(ctx, []byte("x"), nil); newID != "k2" {
		t.Fatalf("new data keyID = %s", newID)
	}
}
