// Package httpbasic defines the HTTP Basic credential type.
package httpbasic

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.http.basic"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "HTTP Basic Auth", Icon: "key",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthBasic, UserField: "user", PasswordField: "password"},
	}
}
