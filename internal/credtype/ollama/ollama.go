// Package ollama defines the Ollama server credential type.
package ollama

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.ollama"

// Credential field names.
const (
	FieldBaseURL    = "baseUrl"
	FieldAPIKey     = "apiKey"
	FieldHeaderName = "headerName"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Ollama", Icon: "server",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldBaseURL, DisplayName: "Base URL", Type: nodesdk.PropString,
				Description: "Optional. Defaults to nodes.ai.ollamaBaseUrl (http://localhost:11434)."}},
			{Property: nodesdk.Property{Name: FieldAPIKey, DisplayName: "API key", Type: nodesdk.PropString,
				Description: "Optional. For an Ollama server behind an authenticating proxy."}, Secret: true},
			{Property: nodesdk.Property{Name: FieldHeaderName, DisplayName: "Header name", Type: nodesdk.PropString,
				Description: "Optional. Send the API key in this header instead of Authorization: Bearer."}},
		},
	}
}
