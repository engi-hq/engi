// Package argon2id implements port.PasswordHasher with argon2id
// (golang.org/x/crypto, BSD-3-Clause) and the PHC string format, so every hash
// carries the parameters it was made with.
package argon2id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
)

// ErrInvalidHash is returned for a hash that is not a supported argon2id string.
var ErrInvalidHash = errors.New("invalid argon2id hash")

// Hasher hashes passwords with fixed cost parameters.
type Hasher struct {
	p      config.Argon2Config
	random io.Reader
	// slots bounds concurrent derivations; each holds MemoryKiB of RAM.
	slots chan struct{}
}

var _ port.PasswordHasher = (*Hasher)(nil)

// New returns a Hasher. random is the salt source; nil selects crypto/rand.
func New(p config.Argon2Config, random io.Reader) *Hasher {
	if random == nil {
		random = rand.Reader
	}
	return &Hasher{p: p, random: random, slots: make(chan struct{}, max(p.MaxConcurrent, 1))}
}

// derive runs argon2id once, waiting for a free slot first.
func (h *Hasher) derive(password string, salt []byte, c config.Argon2Config) []byte {
	h.slots <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, c.Iterations, c.MemoryKiB, c.Threads, c.KeyBytes)
	<-h.slots
	if h.p.ReleaseMemory && len(h.slots) == 0 {
		debug.FreeOSMemory()
	}
	return key
}

var enc = base64.RawStdEncoding

// Hash implements port.PasswordHasher.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.p.SaltBytes)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	key := h.derive(password, salt, h.p)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.p.MemoryKiB, h.p.Iterations, h.p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

type parsed struct {
	cfg  config.Argon2Config
	salt []byte
	key  []byte
}

func parse(encoded string) (parsed, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return parsed{}, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return parsed{}, ErrInvalidHash
	}
	var p parsed
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.cfg.MemoryKiB, &p.cfg.Iterations, &p.cfg.Threads); err != nil {
		return parsed{}, ErrInvalidHash
	}
	var err error
	if p.salt, err = enc.DecodeString(parts[4]); err != nil {
		return parsed{}, ErrInvalidHash
	}
	if p.key, err = enc.DecodeString(parts[5]); err != nil || len(p.key) == 0 {
		return parsed{}, ErrInvalidHash
	}
	p.cfg.SaltBytes, p.cfg.KeyBytes = uint32(len(p.salt)), uint32(len(p.key)) //nolint:gosec // lengths of decoded short strings
	return p, nil
}

// Verify implements port.PasswordHasher.
func (h *Hasher) Verify(password, encoded string) (bool, error) {
	p, err := parse(encoded)
	if err != nil {
		return false, err
	}
	key := h.derive(password, p.salt, p.cfg)
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

// NeedsRehash implements port.PasswordHasher.
func (h *Hasher) NeedsRehash(encoded string) bool {
	p, err := parse(encoded)
	return err != nil || p.cfg.Iterations != h.p.Iterations || p.cfg.MemoryKiB != h.p.MemoryKiB || p.cfg.Threads != h.p.Threads ||
		p.cfg.SaltBytes != h.p.SaltBytes || p.cfg.KeyBytes != h.p.KeyBytes
}
