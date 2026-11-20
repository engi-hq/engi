// Package postgres defines the Postgres connection credential type. It only
// declares fields; the Postgres node arrives with the data-source phase.
package postgres

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.postgres"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	opts := func(vals ...string) []nodesdk.Option {
		out := make([]nodesdk.Option, len(vals))
		for i, v := range vals {
			out[i] = nodesdk.Option{Name: v, Value: v}
		}
		return out
	}
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Postgres", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(5432)}},
			{Property: nodesdk.Property{Name: "database", DisplayName: "Database", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: "sslMode", DisplayName: "SSL mode", Type: nodesdk.PropOptions, Default: "prefer",
				Options: opts("disable", "allow", "prefer", "require", "verify-ca", "verify-full")}},
		},
	}
}
