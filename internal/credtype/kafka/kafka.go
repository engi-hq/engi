// Package kafka defines the Kafka connection credential type used by the Kafka
// trigger and producer nodes.
package kafka

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.kafka"

// SASL mechanisms (field saslMechanism).
const (
	SASLNone        = "none"
	SASLPlain       = "plain"
	SASLScram256    = "scram-sha-256"
	SASLScram512    = "scram-sha-512"
	defaultClientID = "engi"
)

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	opt := func(v, name string) nodesdk.Option { return nodesdk.Option{Name: name, Value: v} }
	sasl := &nodesdk.DisplayOptions{Show: map[string][]any{"saslMechanism": {SASLPlain, SASLScram256, SASLScram512}}}
	tls := &nodesdk.DisplayOptions{Show: map[string][]any{"tls": {true}}}
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Kafka", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "brokers", DisplayName: "Brokers", Type: nodesdk.PropString, Required: true,
				Placeholder: "broker1:9092,broker2:9092", Description: "Comma-separated host:port list."}},
			{Property: nodesdk.Property{Name: "clientId", DisplayName: "Client ID", Type: nodesdk.PropString, Default: defaultClientID}},
			{Property: nodesdk.Property{Name: "saslMechanism", DisplayName: "SASL mechanism", Type: nodesdk.PropOptions, Default: SASLNone,
				Options: []nodesdk.Option{opt(SASLNone, "None"), opt(SASLPlain, "PLAIN"), opt(SASLScram256, "SCRAM-SHA-256"), opt(SASLScram512, "SCRAM-SHA-512")}}},
			{Property: nodesdk.Property{Name: "username", DisplayName: "Username", Type: nodesdk.PropString, DisplayOptions: sasl}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString, DisplayOptions: sasl}, Secret: true},
			{Property: nodesdk.Property{Name: "tls", DisplayName: "Use TLS", Type: nodesdk.PropBoolean, Default: false}},
			{Property: nodesdk.Property{Name: "caCertificate", DisplayName: "CA certificate (PEM)", Type: nodesdk.PropString, DisplayOptions: tls,
				Description: "Trusts this CA instead of the system roots. Leave empty for public brokers."}},
			{Property: nodesdk.Property{Name: "tlsSkipVerify", DisplayName: "Skip certificate verification", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: tls,
				Description: "Development only: accepts any broker certificate."}},
		},
	}
}
