//go:build integration

package s3_test

import (
	"context"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	blobs3 "github.com/engi-hq/engi/internal/adapter/blob/s3"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
)

// The official minio/minio images are no longer published; Chainguard ships a maintained build.
const minioImage = "cgr.dev/chainguard/minio:latest"

func TestContractOnMinIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c, err := minio.Run(ctx, minioImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting minio: %v", err)
	}
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.BlobS3Config{
		Bucket: "engi-blobs", Region: "us-east-1", Endpoint: "http://" + addr, ForcePathStyle: true,
		AccessKeyID: c.Username, SecretAccessKey: config.Secret(c.Password), PartSize: 5 << 20, ConnectTimeout: 10 * time.Second,
	}
	admin := awss3.New(awss3.Options{
		Region: cfg.Region, BaseEndpoint: awsconfig.String(cfg.Endpoint), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(c.Username, c.Password, ""),
	})
	if _, err := admin.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: &cfg.Bucket}); err != nil {
		t.Fatalf("creating bucket: %v", err)
	}
	n := 0
	porttest.RunBlobStore(t, func(t *testing.T) port.BlobStore {
		n++
		cfg := cfg
		cfg.Prefix = "t" + string(rune('a'+n)) + "/" // isolate cases in one bucket
		s, err := blobs3.New(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}
