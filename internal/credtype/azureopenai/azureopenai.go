// Package azureopenai defines the Azure OpenAI credential type.
package azureopenai

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.azureOpenAI"

// Credential field names.
const (
	FieldAPIKey     = "apiKey"
	FieldEndpoint   = "endpoint"
	FieldAPIVersion = "apiVersion"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Azure OpenAI", Icon: "sparkles",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldAPIKey, DisplayName: "API key", Type: nodesdk.PropString, Required: true}, Secret: true},
			{Property: nodesdk.Property{Name: FieldEndpoint, DisplayName: "Resource endpoint", Type: nodesdk.PropString, Required: true,
				Description: "For example https://my-resource.openai.azure.com. The node's model is the deployment name."}},
			{Property: nodesdk.Property{Name: FieldAPIVersion, DisplayName: "API version", Type: nodesdk.PropString,
				Description: "Optional. Overrides nodes.ai.azureApiVersion."}},
		},
	}
}
