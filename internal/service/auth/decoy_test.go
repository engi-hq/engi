package auth_test

import (
	"crypto/rand"
	"testing"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/service/auth"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

// countingHasher counts Hash calls on top of the plain test hasher.
type countingHasher struct {
	plainHasher
	hashes *int
}

func (h countingHasher) Hash(p string) (string, error) { *h.hashes++; return h.plainHasher.Hash(p) }

func TestNoPasswordIsHashedUntilNeeded(t *testing.T) {
	var n int
	clk := servicetest.NewClock()
	st := newMemStore()
	svc, err := auth.New(config.Default().Auth, auth.Deps{
		Users: users{st}, Sessions: sessions{st}, APIKeys: apiKeys{st}, Hasher: countingHasher{plainHasher{"v1"}, &n},
		Tx: noTx{}, Clock: clk, IDs: id.NewGenerator(clk.Now, rand.Reader),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("New hashed %d passwords; argon2 memory must not be spent at startup", n)
	}
	_, _ = svc.Login(t.Context(), "nobody@example.com", goodPassword, "10.0.0.1")
	_, _ = svc.Login(t.Context(), "nobody@example.com", goodPassword, "10.0.0.1")
	if n != 1 {
		t.Fatalf("decoy hashed %d times, want once", n)
	}
}
