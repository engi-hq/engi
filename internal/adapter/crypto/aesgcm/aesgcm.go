// Package aesgcm implements port.Cipher with AES-256-GCM and a keyring.
//
// Ciphertext layout: nonce (12 bytes) || sealed data. A fresh random nonce is
// used for every encryption; the caller's additional data is authenticated.
package aesgcm

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// KeySize is the required key length in bytes (AES-256).
const KeySize = 32

// ErrUnknownKey is returned when ciphertext names a key that is not in the keyring.
var ErrUnknownKey = errors.New("unknown encryption key id")

// ErrDecrypt is returned when ciphertext cannot be opened: wrong key, wrong
// additional data or tampered bytes. It deliberately says nothing more.
var ErrDecrypt = errors.New("decryption failed")

// Cipher is a keyring-backed AES-256-GCM cipher. It is safe for concurrent use.
type Cipher struct {
	active string
	aeads  map[string]cipher.AEAD
	random io.Reader
}

var _ port.Cipher = (*Cipher)(nil)

// New builds a Cipher from the credential configuration. It fails with a clear
// message when the keyring is missing, a key is not base64 or not 32 bytes.
func New(cfg config.CredentialsConfig) (*Cipher, error) {
	return NewWithRandom(cfg, rand.Reader)
}

// NewWithRandom is New with an injected entropy source, for tests.
func NewWithRandom(cfg config.CredentialsConfig, random io.Reader) (*Cipher, error) {
	if err := cfg.RequireKeyring(); err != nil {
		return nil, fmt.Errorf("building cipher: %w", err)
	}
	ids := make([]string, 0, len(cfg.Keys))
	for id := range cfg.Keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	c := &Cipher{active: cfg.ActiveKeyID, aeads: make(map[string]cipher.AEAD, len(ids)), random: random}
	for _, id := range ids {
		raw, err := base64.StdEncoding.DecodeString(cfg.Keys[id].Reveal())
		if err != nil {
			return nil, fmt.Errorf("encryption key %q is not valid base64", id)
		}
		if len(raw) != KeySize {
			return nil, fmt.Errorf("encryption key %q must be %d bytes, got %d", id, KeySize, len(raw))
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, fmt.Errorf("encryption key %q: %w", id, err)
		}
		if c.aeads[id], err = cipher.NewGCM(block); err != nil {
			return nil, fmt.Errorf("encryption key %q: %w", id, err)
		}
	}
	return c, nil
}

// ActiveKeyID implements port.Cipher.
func (c *Cipher) ActiveKeyID() string { return c.active }

// Encrypt implements port.Cipher.
func (c *Cipher) Encrypt(ctx context.Context, plaintext, aad []byte) (keyID string, ciphertext []byte, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("encrypting: %w", err)
	}
	aead := c.aeads[c.active]
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return "", nil, fmt.Errorf("generating nonce: %w", err)
	}
	return c.active, aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Decrypt implements port.Cipher.
func (c *Cipher) Decrypt(ctx context.Context, keyID string, ciphertext, aad []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	aead, ok := c.aeads[keyID]
	if !ok {
		return nil, fmt.Errorf("key %q: %w", keyID, ErrUnknownKey)
	}
	n := aead.NonceSize()
	if len(ciphertext) < n+aead.Overhead() {
		return nil, ErrDecrypt
	}
	plain, err := aead.Open(nil, ciphertext[:n], ciphertext[n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
