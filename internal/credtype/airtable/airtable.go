// Package airtable defines the Airtable credential type (a personal access token).
package airtable

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.airtable"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Airtable", Icon: "table",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "token", DisplayName: "Personal access token", Type: nodesdk.PropString, Required: true,
				Description: "A personal access token, e.g. pat..."}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthBearer, TokenField: "token"},
	}
}
