// Package querykey defines the API Key in Query String credential type.
package querykey

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.http.queryKey"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "API Key in Query", Icon: "key",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "name", DisplayName: "Parameter name", Type: nodesdk.PropString, Required: true, Default: "api_key"}},
			{Property: nodesdk.Property{Name: "value", DisplayName: "API key", Type: nodesdk.PropString, Required: true}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthQuery, NameField: "name", ValueField: "value"},
	}
}
