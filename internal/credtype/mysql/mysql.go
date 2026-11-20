// Package mysql defines the MySQL connection credential type.
package mysql

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.mysql"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "MySQL", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(3306)}},
			{Property: nodesdk.Property{Name: "database", DisplayName: "Database", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: "tlsMode", DisplayName: "TLS mode", Type: nodesdk.PropOptions, Default: "disable",
				Description: "required verifies the server certificate and host name.",
				Options: []nodesdk.Option{
					{Name: "Disabled", Value: "disable"}, {Name: "Required (verify)", Value: "required"},
					{Name: "Required (skip verification)", Value: "skip-verify"},
				}}},
			{Property: nodesdk.Property{Name: "caCertificate", DisplayName: "CA certificate (PEM)", Type: nodesdk.PropString,
				Description: "Trusted CA for the server certificate; empty uses the system roots."}, Secret: true},
		},
	}
}
