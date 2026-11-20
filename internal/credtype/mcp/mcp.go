// Package mcp defines the MCP server credential type: how Engi authenticates to
// a remote Model Context Protocol server.
package mcp

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.mcp"

// Credential field names.
const (
	FieldAuthentication = "authentication"
	FieldToken          = "token"
	FieldHeaderName     = "headerName"
	FieldHeaderValue    = "headerValue"
)

// Authentication kinds of the authentication field.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthHeader = "header"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "MCP Server", Icon: "plug",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: FieldAuthentication, DisplayName: "Authentication", Type: nodesdk.PropOptions, Default: AuthBearer,
				Options: []nodesdk.Option{
					{Name: "None", Value: AuthNone},
					{Name: "Bearer token", Value: AuthBearer, Description: "Sent as Authorization: Bearer <token>."},
					{Name: "Custom header", Value: AuthHeader, Description: "Sent as <header name>: <header value>."},
				}}},
			{Property: nodesdk.Property{Name: FieldToken, DisplayName: "Token", Type: nodesdk.PropString,
				DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{FieldAuthentication: {AuthBearer}}}}, Secret: true},
			{Property: nodesdk.Property{Name: FieldHeaderName, DisplayName: "Header name", Type: nodesdk.PropString, Placeholder: "X-API-Key",
				DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{FieldAuthentication: {AuthHeader}}}}},
			{Property: nodesdk.Property{Name: FieldHeaderValue, DisplayName: "Header value", Type: nodesdk.PropString,
				DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{FieldAuthentication: {AuthHeader}}}}, Secret: true},
		},
	}
}
