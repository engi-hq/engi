// Package redis defines the Redis connection credential type used by the Redis node.
package redis

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.redis"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "Redis", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "host", DisplayName: "Host", Type: nodesdk.PropString, Default: "localhost", Required: true}},
			{Property: nodesdk.Property{Name: "port", DisplayName: "Port", Type: nodesdk.PropNumber, Default: float64(6379)}},
			{Property: nodesdk.Property{Name: "db", DisplayName: "Database index", Type: nodesdk.PropNumber, Default: float64(0)}},
			{Property: nodesdk.Property{Name: "username", DisplayName: "Username", Type: nodesdk.PropString}},
			{Property: nodesdk.Property{Name: "password", DisplayName: "Password", Type: nodesdk.PropString}, Secret: true},
			{Property: nodesdk.Property{Name: "tls", DisplayName: "Use TLS", Type: nodesdk.PropBoolean, Default: false}},
		},
	}
}
