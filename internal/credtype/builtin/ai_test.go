package builtin_test

import (
	"testing"

	"github.com/engi-hq/engi/internal/credtype/builtin"
)

func TestAICredentialTypes(t *testing.T) {
	secrets := map[string][]string{
		"engi.anthropic": {"apiKey"}, "engi.openai": {"apiKey"}, "engi.azureOpenAI": {"apiKey"}, "engi.ollama": {"apiKey"},
	}
	required := map[string][]string{
		"engi.anthropic": {"apiKey"}, "engi.openai": {"apiKey"}, "engi.azureOpenAI": {"apiKey", "endpoint"}, "engi.ollama": nil,
	}
	found := map[string]bool{}
	for _, ct := range builtin.Types() {
		want, ok := secrets[ct.Name]
		if !ok {
			continue
		}
		found[ct.Name] = true
		if err := ct.Validate(); err != nil {
			t.Errorf("%s: %v", ct.Name, err)
		}
		isSecret, isRequired := map[string]bool{}, map[string]bool{}
		for _, f := range ct.Fields {
			if f.Secret {
				isSecret[f.Name] = true
			}
			if f.Required {
				isRequired[f.Name] = true
			}
		}
		for _, name := range want {
			if !isSecret[name] {
				t.Errorf("%s.%s must be secret", ct.Name, name)
			}
		}
		if len(isSecret) != len(want) {
			t.Errorf("%s: unexpected secret fields %v", ct.Name, isSecret)
		}
		if len(isRequired) != len(required[ct.Name]) {
			t.Errorf("%s: required = %v, want %v", ct.Name, isRequired, required[ct.Name])
		}
		for _, name := range required[ct.Name] {
			if !isRequired[name] {
				t.Errorf("%s.%s must be required", ct.Name, name)
			}
		}
	}
	if len(found) != len(secrets) {
		t.Fatalf("found %v", found)
	}
}
