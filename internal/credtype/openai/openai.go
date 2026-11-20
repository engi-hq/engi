// Package openai defines the OpenAI API credential type. It also fits any
// server that speaks the OpenAI chat API when the base URL is set.
package openai

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.openai"

// Credential field names.
const (
	FieldAPIKey       = "apiKey"
	FieldBaseURL      = "baseUrl"
	FieldOrganization = "organization"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "OpenAI", Icon: "sparkles",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldAPIKey, DisplayName: "API key", Type: nodesdk.PropString, Required: true}, Secret: true},
			{Property: nodesdk.Property{Name: FieldBaseURL, DisplayName: "Base URL", Type: nodesdk.PropString,
				Description: "Optional. Overrides nodes.ai.openaiBaseUrl, for a compatible server or gateway."}},
			{Property: nodesdk.Property{Name: FieldOrganization, DisplayName: "Organization ID", Type: nodesdk.PropString,
				Description: "Optional. Sent as the OpenAI-Organization header."}},
		},
	}
}
