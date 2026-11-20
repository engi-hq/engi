package port

import "context"

// Cipher encrypts and decrypts secrets with a keyring. The additional data
// (aad) is authenticated but not stored: decryption fails unless it matches
// the value given at encryption, which binds ciphertext to its owner.
type Cipher interface {
	// Encrypt seals plaintext with the active key and returns that key's ID.
	Encrypt(ctx context.Context, plaintext, aad []byte) (keyID string, ciphertext []byte, err error)
	// Decrypt opens ciphertext sealed under keyID.
	Decrypt(ctx context.Context, keyID string, ciphertext, aad []byte) ([]byte, error)
	// ActiveKeyID is the key new data is encrypted with.
	ActiveKeyID() string
}
