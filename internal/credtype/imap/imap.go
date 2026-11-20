// Package imap defines the IMAP connection credential type used by the IMAP
// trigger.
package imap

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.imap"

// Connection security modes (field security).
const (
	// SecurityNone connects in clear text.
	SecurityNone = "none"
	// SecuritySTARTTLS upgrades a plain connection with STARTTLS (port 143).
	SecuritySTARTTLS = "starttls"
	// SecurityTLS wraps the connection in TLS from the start (port 993).
	SecurityTLS = "tls"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	opt := func(v, name string) nodesdk.Option { return nodesdk.Option{Name: name, Value: v} }
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "IMAP", Icon: "mail",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(993)}},
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString, Required: true}, Secret: true},
			{Property: nodesdk.Property{Name: "security", DisplayName: "Security", Type: nodesdk.PropOptions, Default: SecurityTLS,
				Options: []nodesdk.Option{opt(SecurityNone, "None"), opt(SecuritySTARTTLS, "STARTTLS"), opt(SecurityTLS, "TLS")}}},
		},
	}
}
