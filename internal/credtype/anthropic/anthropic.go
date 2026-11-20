// Package anthropic defines the Anthropic API credential type.
package anthropic

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.anthropic"

// Credential field names.
const (
	FieldAPIKey  = "apiKey"
	FieldBaseURL = "baseUrl"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Anthropic", Icon: "sparkles",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldAPIKey, DisplayName: "API key", Type: nodesdk.PropString, Required: true}, Secret: true},
			{Property: nodesdk.Property{Name: FieldBaseURL, DisplayName: "Base URL", Type: nodesdk.PropString,
				Description: "Optional. Overrides nodes.ai.anthropicBaseUrl, for a proxy or gateway."}},
		},
	}
}
