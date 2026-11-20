// Package oauth2 defines the generic OAuth2 credential type. Token handling
// (fetch, refresh, storage) lives in the credential service; this package only
// declares the schema and how the access token authenticates a request.
package oauth2

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.oauth2"

// Field names, shared with the credential service through nodesdk.OAuth2Spec.
const (
	FieldGrant        = "grantType"
	FieldAuthURL      = "authUrl"
	FieldTokenURL     = "accessTokenUrl"
	FieldClientID     = "clientId"
	FieldClientSecret = "clientSecret"
	FieldScope        = "scope"
	FieldAuthStyle    = "authentication"
	FieldToken        = "oauthTokenData" //nolint:gosec // a field name, not a credential
	FieldAccess       = "accessToken"
)

// Authentication styles for the token endpoint.
const (
	StyleHeader = "header"
	StyleBody   = "body"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	authCode := &nodesdk.DisplayOptions{Show: map[string][]any{FieldGrant: {string(nodesdk.GrantAuthorizationCode)}}}
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "OAuth2", Icon: "shield",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldGrant, DisplayName: "Grant type", Type: nodesdk.PropOptions,
				Default: string(nodesdk.GrantClientCredentials), Required: true, Options: []nodesdk.Option{
					{Name: "Client credentials", Value: string(nodesdk.GrantClientCredentials)},
					{Name: "Authorization code (PKCE)", Value: string(nodesdk.GrantAuthorizationCode)},
				}}},
			{Property: nodesdk.Property{Name: FieldAuthURL, DisplayName: "Authorization URL", Type: nodesdk.PropString,
				Required: true, DisplayOptions: authCode}},
			{Property: nodesdk.Property{Name: FieldTokenURL, DisplayName: "Access token URL", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: FieldClientID, DisplayName: "Client ID", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: FieldClientSecret, DisplayName: "Client secret", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: FieldScope, DisplayName: "Scope", Type: nodesdk.PropString, Description: "Space separated."}},
			{Property: nodesdk.Property{Name: FieldAuthStyle, DisplayName: "Send client credentials", Type: nodesdk.PropOptions, Default: StyleHeader,
				Options: []nodesdk.Option{{Name: "In Authorization header (Basic)", Value: StyleHeader}, {Name: "In request body", Value: StyleBody}}}},
			{Property: nodesdk.Property{Name: FieldToken, DisplayName: "Token data", Type: nodesdk.PropJSON}, Secret: true, Managed: true},
		},
		Authenticate: &nodesdk.AuthSpec{Kind: nodesdk.AuthBearer, TokenField: FieldAccess},
		OAuth2: &nodesdk.OAuth2Spec{
			GrantField: FieldGrant, AuthURLField: FieldAuthURL, TokenURLField: FieldTokenURL,
			ClientIDField: FieldClientID, ClientSecretField: FieldClientSecret, ScopeField: FieldScope,
			AuthStyleField: FieldAuthStyle, TokenField: FieldToken, AccessField: FieldAccess,
		},
	}
}
