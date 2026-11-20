package postgres

import "testing"

// TestHashIsPinned guards the key-to-lock mapping: replicas on different
// releases must derive the same advisory lock for the same credential.
func TestHashIsPinned(t *testing.T) {
	const want int64 = -3662657460372754012
	if got := Hash("engi:oauth2:cred_x"); got != want {
		t.Fatalf("Hash = %d, want %d", got, want)
	}
	if Hash("engi:oauth2:cred_x") == Hash("engi:oauth2:cred_y") {
		t.Fatal("distinct keys hash equal")
	}
}
