package builtin_test

import (
	"net/http"
	"testing"

	"github.com/engi-hq/engi/internal/credtype/builtin"
)

func TestAirtableAndServiceAccountTypes(t *testing.T) {
	found := map[string]bool{}
	for _, ct := range builtin.Types() {
		switch ct.Name {
		case "engi.airtable":
			found[ct.Name] = true
			req, _ := http.NewRequest(http.MethodGet, "http://x.test", http.NoBody)
			if err := ct.Authenticate.Authenticate(req, map[string]any{"token": "pat1"}); err != nil || req.Header.Get("Authorization") != "Bearer pat1" {
				t.Errorf("airtable auth: %v %q", err, req.Header.Get("Authorization"))
			}
			if len(ct.Fields) != 1 || !ct.Fields[0].Secret {
				t.Errorf("airtable token must be the single secret field")
			}
		case "engi.googleServiceAccount":
			found[ct.Name] = true
			for _, f := range ct.Fields {
				if f.Secret != (f.Name == "privateKey") {
					t.Errorf("field %s secret = %v", f.Name, f.Secret)
				}
			}
		default:
			continue
		}
		if err := ct.Validate(); err != nil {
			t.Errorf("%s: %v", ct.Name, err)
		}
	}
	if len(found) != 2 {
		t.Fatalf("found %v", found)
	}
}
