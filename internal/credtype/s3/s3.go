// Package s3 defines the S3-compatible object storage credential type used by
// the S3 node.
package s3

import "github.com/engi-hq/engi/pkg/nodesdk"

// Name is the credential type id.
const Name = "engi.s3"

// DefaultRegion is pre-filled in the credential form.
const DefaultRegion = "us-east-1"

// Type returns the credential type.
func Type() nodesdk.CredentialType {
	return nodesdk.CredentialType{
		Name: Name, DisplayName: "S3", Icon: "database",
		Fields: []nodesdk.CredentialField{
			{Property: nodesdk.Property{Name: "endpoint", DisplayName: "Endpoint", Type: nodesdk.PropString,
				Placeholder: "https://minio.example.com:9000", Description: "Leave empty for AWS S3. Set it for MinIO, Cloudflare R2, Backblaze B2 and other S3-compatible stores."}},
			{Property: nodesdk.Property{Name: "region", DisplayName: "Region", Type: nodesdk.PropString, Required: true, Default: DefaultRegion}},
			{Property: nodesdk.Property{Name: "accessKeyId", DisplayName: "Access key ID", Type: nodesdk.PropString, Required: true}},
			{Property: nodesdk.Property{Name: "secretAccessKey", DisplayName: "Secret access key", Type: nodesdk.PropString, Required: true}, Secret: true},
			{Property: nodesdk.Property{Name: "sessionToken", DisplayName: "Session token", Type: nodesdk.PropString,
				Description: "Only for temporary credentials."}, Secret: true},
			{Property: nodesdk.Property{Name: "forcePathStyle", DisplayName: "Force path-style addressing", Type: nodesdk.PropBoolean, Default: false,
				Description: "Use endpoint/bucket/key instead of bucket.endpoint/key. Most self-hosted stores need this."}},
			{Property: nodesdk.Property{Name: "tlsSkipVerify", DisplayName: "Skip certificate verification", Type: nodesdk.PropBoolean, Default: false,
				Description: "Development only: accepts any server certificate."}},
		},
	}
}
