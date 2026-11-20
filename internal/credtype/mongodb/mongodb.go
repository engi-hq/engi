// Package mongodb defines the MongoDB connection credential type used by the MongoDB node.
package mongodb

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.mongodb"

// Type returns the credential type. A connection string, when set, wins over
// the host, port, user and password fields.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "MongoDB", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "connectionString", DisplayName: "Connection string", Type: nodesdk.PropString,
				Description: "mongodb:// or mongodb+srv:// URI. When set, host, port, user and password are ignored."}, Secret: true},
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(27017)}},
			{Property: nodesdk.Property{Name: "user", DisplayName: "User", Type: nodesdk.PropString}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: "database", DisplayName: "Database", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "tls", DisplayName: "Use TLS", Type: nodesdk.PropBoolean, Default: false}},
		},
	}
}
