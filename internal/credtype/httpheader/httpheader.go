// Package httpheader defines the HTTP Header credential type (a name/value pair
// sent as a request header).
package httpheader

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.http.header"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "HTTP Header Auth", Icon: "key",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "name", DisplayName: "Header name", Type: nodesdk.PropString, Required: true, Placeholder: "X-API-Key"}},
			{Property: nodesdk.Property{Name: "value", DisplayName: "Header value", Type: nodesdk.PropString, Required: true}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthHeader, NameField: "name", ValueField: "value"},
	}
}
