package argon2id_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/crypto/argon2id"
	"github.com/engi-hq/engi/internal/config"
)

func fast() config.Argon2Config {
	return config.Argon2Config{Iterations: 1, MemoryKiB: 8, Threads: 1, SaltBytes: 16, KeyBytes: 32, MaxConcurrent: 2}
}

func TestHashVerify(t *testing.T) {
	h := argon2id.New(fast(), nil)
	enc, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("unexpected encoding %q", enc)
	}
	again, _ := h.Hash("correct horse")
	if enc == again {
		t.Fatal("two hashes of one password must differ (random salt)")
	}
	tests := []struct {
		name, password string
		want           bool
	}{
		{"right password", "correct horse", true},
		{"wrong password", "battery staple", false},
		{"empty password", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := h.Verify(tt.password, enc)
			if err != nil || ok != tt.want {
				t.Fatalf("Verify = %v, %v; want %v", ok, err, tt.want)
			}
		})
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	h := argon2id.New(fast(), nil)
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=8,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=x$AAAA$AAAA", "$argon2id$v=19$m=8,t=1,p=1$!!$AAAA"} {
		if _, err := h.Verify("x", bad); !errors.Is(err, argon2id.ErrInvalidHash) {
			t.Errorf("Verify(%q) error = %v, want ErrInvalidHash", bad, err)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	old := argon2id.New(fast(), nil)
	enc, _ := old.Hash("pw")
	if old.NeedsRehash(enc) {
		t.Error("hash with current parameters must not need a rehash")
	}
	stronger := fast()
	stronger.Iterations = 2
	if !argon2id.New(stronger, nil).NeedsRehash(enc) {
		t.Error("hash with weaker parameters must need a rehash")
	}
	if !old.NeedsRehash("garbage") {
		t.Error("garbage must need a rehash")
	}
}

func TestConcurrencyLimitStillCompletesAndRehashIgnoresIt(t *testing.T) {
	p := fast()
	p.MaxConcurrent = 1
	h := argon2id.New(p, nil)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			enc, err := h.Hash("pw")
			if err != nil {
				t.Error(err)
				return
			}
			if ok, err := h.Verify("pw", enc); err != nil || !ok {
				t.Errorf("verify = %v, %v", ok, err)
			}
		})
	}
	wg.Wait()
	enc, _ := h.Hash("pw")
	p.MaxConcurrent = 7
	if argon2id.New(p, nil).NeedsRehash(enc) {
		t.Fatal("changing maxConcurrent must not force a rehash")
	}
}
