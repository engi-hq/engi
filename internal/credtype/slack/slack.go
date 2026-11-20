// Package slack defines the Slack credential type (a bot or user token).
package slack

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.slack"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Slack", Icon: "message-square",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "token", DisplayName: "Bot token", Type: nodesdk.PropString, Required: true,
				Description: "A bot or user token, e.g. xoxb-..."}, Secret: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthBearer, TokenField: "token"},
	}
}
