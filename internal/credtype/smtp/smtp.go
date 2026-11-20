// Package smtp defines the SMTP connection credential type used by the Send
// Email node.
package smtp

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.smtp"

// Connection security modes (field security).
const (
	// SecurityNone sends in clear text.
	SecurityNone = "none"
	// SecuritySTARTTLS upgrades a plain connection with STARTTLS (port 587).
	SecuritySTARTTLS = "starttls"
	// SecurityTLS wraps the connection in TLS from the start (port 465).
	SecurityTLS = "tls"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	opt := func(v, name string) nodesdk.Option { return nodesdk.Option{Name: name, Value: v} }
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "SMTP", Icon: "mail",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(587)}},
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString,
				Description: "Leave empty for servers that need no authentication."}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: "security", DisplayName: "Security", Type: nodesdk.PropOptions, Default: SecuritySTARTTLS,
				Options: []nodesdk.Option{opt(SecurityNone, "None"), opt(SecuritySTARTTLS, "STARTTLS"), opt(SecurityTLS, "TLS")}}},
			{Property: nodesdk.Property{Name: "fromAddress", DisplayName: "Default from address", Type: nodesdk.PropString,
				Placeholder: "Engi <noreply@example.com>", Description: "Used when the node sets no From."}},
		},
	}
}
