// Package googleserviceaccount defines the Google service account credential
// type. It only stores the key; the node that uses it signs a JWT with the
// private key and exchanges it for an access token (see ServiceAccount.Token
// in the Google Sheets node), so the type declares no request authentication.
package googleserviceaccount

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.googleServiceAccount"

// Field names read by the nodes that use this credential.
const (
	FieldEmail      = "email"
	FieldPrivateKey = "privateKey"
	FieldTokenURL   = "tokenUrl"
	FieldSubject    = "subject"
)

// DefaultTokenURL is Google's OAuth2 token endpoint, the default of the tokenUrl field.
const DefaultTokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // a public endpoint, not a credential

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Google service account", Icon: "key",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldEmail, DisplayName: "Service account email", Type: nodesdk.PropString, Required: true,
				Description: "The client_email of the service account key file."}},
			{Property: nodesdk.Property{Name: FieldPrivateKey, DisplayName: "Private key", Type: nodesdk.PropString, Required: true,
				Description: "The private_key (PEM) of the key file."}, Secret: true},
			{Property: nodesdk.Property{Name: FieldTokenURL, DisplayName: "Token URL", Type: nodesdk.PropString, Default: DefaultTokenURL,
				Description: "Token endpoint; the token_uri of the key file."}},
			{Property: nodesdk.Property{Name: FieldSubject, DisplayName: "Impersonate user", Type: nodesdk.PropString,
				Description: "Optional. Email of a Workspace user to act as (domain-wide delegation)."}},
		},
	}
}
