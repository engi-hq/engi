// Package bearertoken defines the Bearer Token credential type.
package bearertoken

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.http.bearer"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Bearer Token", Icon: "key",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "token", DisplayName: "Token", Type: nodesdk.PropString, Required: true}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthBearer, TokenField: "token"},
	}
}
