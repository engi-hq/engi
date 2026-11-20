package app

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryRouteIsInTheOpenAPISpec keeps docs/api/openapi.yaml honest in both
// directions: no registered route may be missing from it, and it may not
// describe a route the server does not have.
func TestEveryRouteIsInTheOpenAPISpec(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x", spec.OpenAPI)
	}
	documented := map[string]bool{}
	methods := []string{"get", "put", "post", "delete", "patch", "options", "head"}
	for path, item := range spec.Paths {
		for key := range item {
			if slices.Contains(methods, key) {
				documented[strings.ToUpper(key)+" "+path] = true
			}
		}
	}

	f := newServerFixture(t)
	registered := map[string]bool{}
	for _, r := range f.srv.API.Routes() {
		registered[r] = true
		if !documented[r] {
			t.Errorf("route %q is not documented in docs/api/openapi.yaml", r)
		}
	}
	for d := range documented {
		if !registered[d] {
			t.Errorf("openapi.yaml documents %q, which the server does not register", d)
		}
	}
	if len(registered) < 40 {
		t.Errorf("only %d routes registered", len(registered))
	}
}
