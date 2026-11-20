package s3

import (
	"testing"

	"github.com/engi-hq/engi/internal/config"
)

func TestCredentialsUseStaticKeysOrTheDefaultChain(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.BlobS3Config
		wantID string
	}{
		{"static keys win", config.BlobS3Config{Region: "eu-west-1", AccessKeyID: "static", SecretAccessKey: config.Secret("s")}, "static"},
		// Without keys the default chain applies; its environment provider stands
		// in here for web identity or instance metadata.
		{"default chain", config.BlobS3Config{Region: "eu-west-1"}, "from-env"},
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "from-env")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := credentialsFor(t.Context(), tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Retrieve(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got.AccessKeyID != tt.wantID {
				t.Fatalf("access key id = %q, want %q", got.AccessKeyID, tt.wantID)
			}
		})
	}
}
