package id_test

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/id"
)

func TestGeneratedIDsAreValidAndTyped(t *testing.T) {
	g := id.NewGenerator(time.Now, rand.Reader)
	wf, err := g.WorkflowID()
	if err != nil || wf.Validate() != nil {
		t.Fatalf("workflow id %q: %v / %v", wf, err, wf.Validate())
	}
	ex, _ := g.ExecutionID()
	cr, _ := g.CredentialID()
	nd, _ := g.NodeID()
	for _, c := range []struct {
		got, prefix string
		err         error
	}{
		{string(ex), "ex_", ex.Validate()},
		{string(cr), "cr_", cr.Validate()},
		{string(nd), "nd_", nd.Validate()},
	} {
		if !strings.HasPrefix(c.got, c.prefix) || c.err != nil {
			t.Errorf("id %q invalid: %v", c.got, c.err)
		}
	}
}

func TestIDsAreUniqueAndSortable(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	g := id.NewGenerator(func() time.Time { return fixed }, rand.Reader)
	prev := ""
	for i := 0; i < 1000; i++ {
		cur, err := g.New("ex")
		if err != nil {
			t.Fatal(err)
		}
		if cur <= prev {
			t.Fatalf("id %q is not greater than previous %q", cur, prev)
		}
		prev = cur
	}
}
