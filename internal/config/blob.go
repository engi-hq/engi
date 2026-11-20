package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Blob store drivers.
const (
	BlobDriverFS = "fs"
	BlobDriverS3 = "s3"
)

// BlobConfig configures binary data storage (docs/03 section 3.4). Items carry
// references; the bytes live in the blob store.
type BlobConfig struct {
	// Driver is fs (local directory, the standalone default) or s3 (cluster).
	Driver string `yaml:"driver"`
	// MaxItemBytes caps one binary value.
	MaxItemBytes int64 `yaml:"maxItemBytes"`
	// MaxExecutionBytes caps the binary data of one execution.
	MaxExecutionBytes int64 `yaml:"maxExecutionBytes"`
	// OrphanGrace is how long blobs of an execution that does not exist stay
	// before the sweep after pruning deletes them (uploads precede the
	// execution record).
	OrphanGrace time.Duration `yaml:"orphanGrace"`
	// FS configures the fs driver.
	FS BlobFSConfig `yaml:"fs"`
	// S3 configures the s3 driver. Credentials come from the server
	// configuration, never from a user credential.
	S3 BlobS3Config `yaml:"s3"`
}

// BlobFSConfig configures the fs driver.
type BlobFSConfig struct {
	// Dir is the base directory, created on first write.
	Dir string `yaml:"dir"`
}

// BlobS3Config configures the s3 driver (any S3-compatible store, MinIO included).
type BlobS3Config struct {
	Bucket string `yaml:"bucket"`
	Region string `yaml:"region"`
	// Endpoint overrides the AWS endpoint (MinIO); empty uses AWS.
	Endpoint       string `yaml:"endpoint"`
	ForcePathStyle bool   `yaml:"forcePathStyle"`
	// Prefix is prepended to every object key.
	Prefix string `yaml:"prefix"`
	// AccessKeyID and SecretAccessKey are secrets: supply them by env or file.
	AccessKeyID     string `yaml:"accessKeyId"`
	SecretAccessKey Secret `yaml:"secretAccessKey"`
	// SecretAccessKeyFile is a file holding the secret access key (a mounted
	// Secret); it wins over SecretAccessKey.
	SecretAccessKeyFile string `yaml:"secretAccessKeyFile"`
	// PartSize is the multipart part size; larger objects are uploaded in parts.
	PartSize       int64         `yaml:"partSize"`
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
}

func defaultBlob() BlobConfig {
	return BlobConfig{
		Driver: BlobDriverFS, MaxItemBytes: 256 << 20, MaxExecutionBytes: 1 << 30, OrphanGrace: time.Hour,
		FS: BlobFSConfig{Dir: "blobs"},
		S3: BlobS3Config{Region: "us-east-1", PartSize: 8 << 20, ConnectTimeout: 10 * time.Second},
	}
}

// minS3PartSize is the smallest part size S3 accepts (except for the last part).
const minS3PartSize = 5 << 20

// Validate reports every problem with the blob settings.
func (c BlobConfig) Validate() error {
	var errs []error
	if !slices.Contains([]string{BlobDriverFS, BlobDriverS3}, c.Driver) {
		errs = append(errs, fmt.Errorf("blob.driver %q must be fs or s3", c.Driver))
	}
	errs = appendPositive(errs, "blob.maxItemBytes", c.MaxItemBytes)
	errs = appendPositive(errs, "blob.maxExecutionBytes", c.MaxExecutionBytes)
	errs = appendPositive(errs, "blob.orphanGrace", int64(c.OrphanGrace))
	switch c.Driver {
	case BlobDriverFS:
		if strings.TrimSpace(c.FS.Dir) == "" {
			errs = append(errs, errors.New("blob.fs.dir must not be empty"))
		}
	case BlobDriverS3:
		if c.S3.Bucket == "" {
			errs = append(errs, errors.New("blob.s3.bucket must not be empty"))
		}
		if c.S3.Region == "" {
			errs = append(errs, errors.New("blob.s3.region must not be empty"))
		}
		// Both keys, or neither: then the AWS default credential chain applies
		// (workload identity, instance profile).
		if (c.S3.AccessKeyID == "") != (c.S3.SecretAccessKey == "") {
			errs = append(errs, errors.New("blob.s3.accessKeyId and blob.s3.secretAccessKey must be set together (or both left empty for the default AWS credential chain)"))
		}
		if c.S3.PartSize < minS3PartSize {
			errs = append(errs, fmt.Errorf("blob.s3.partSize must be at least %d", minS3PartSize))
		}
		errs = appendPositive(errs, "blob.s3.connectTimeout", int64(c.S3.ConnectTimeout))
	}
	return errors.Join(errs...)
}

// blobSettings are the env/flag bindings of the blob section.
func blobSettings() []setting {
	i64 := func(dst func(*Config) *int64) func(*Config, string) error {
		return func(c *Config, v string) (err error) { *dst(c), err = strconv.ParseInt(v, 10, 64); return }
	}
	return []setting{
		{"ENGI_BLOB_DRIVER", "blob-driver", "binary data store: fs, s3", stringSetting(func(c *Config) *string { return &c.Blob.Driver })},
		{"ENGI_BLOB_MAX_ITEM_BYTES", "blob-max-item-bytes", "max bytes of one binary value", i64(func(c *Config) *int64 { return &c.Blob.MaxItemBytes })},
		{"ENGI_BLOB_MAX_EXECUTION_BYTES", "blob-max-execution-bytes", "max binary bytes per execution", i64(func(c *Config) *int64 { return &c.Blob.MaxExecutionBytes })},
		{"ENGI_BLOB_ORPHAN_GRACE", "blob-orphan-grace", "age before blobs of a missing execution are swept", durationSetting(func(c *Config) *time.Duration { return &c.Blob.OrphanGrace })},
		{"ENGI_BLOB_FS_DIR", "blob-fs-dir", "fs driver base directory", stringSetting(func(c *Config) *string { return &c.Blob.FS.Dir })},
		{"ENGI_BLOB_S3_BUCKET", "blob-s3-bucket", "s3 driver bucket", stringSetting(func(c *Config) *string { return &c.Blob.S3.Bucket })},
		{"ENGI_BLOB_S3_REGION", "blob-s3-region", "s3 driver region", stringSetting(func(c *Config) *string { return &c.Blob.S3.Region })},
		{"ENGI_BLOB_S3_ENDPOINT", "blob-s3-endpoint", "s3 driver endpoint (MinIO)", stringSetting(func(c *Config) *string { return &c.Blob.S3.Endpoint })},
		{"ENGI_BLOB_S3_FORCE_PATH_STYLE", "blob-s3-force-path-style", "s3 driver path-style addressing", boolSetting(func(c *Config) *bool { return &c.Blob.S3.ForcePathStyle })},
		{"ENGI_BLOB_S3_PREFIX", "blob-s3-prefix", "s3 driver key prefix", stringSetting(func(c *Config) *string { return &c.Blob.S3.Prefix })},
		{"ENGI_BLOB_S3_ACCESS_KEY_ID", "blob-s3-access-key-id", "s3 driver access key id", stringSetting(func(c *Config) *string { return &c.Blob.S3.AccessKeyID })},
		{"ENGI_BLOB_S3_SECRET_ACCESS_KEY", "blob-s3-secret-access-key", "s3 driver secret access key", func(c *Config, v string) error { c.Blob.S3.SecretAccessKey = Secret(v); return nil }},
		{"ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE", "blob-s3-secret-access-key-file", "file holding the s3 secret access key",
			stringSetting(func(c *Config) *string { return &c.Blob.S3.SecretAccessKeyFile })},
		{"ENGI_BLOB_S3_PART_SIZE", "blob-s3-part-size", "s3 multipart part size in bytes", i64(func(c *Config) *int64 { return &c.Blob.S3.PartSize })},
	}
}

// loadSecretFile reads SecretAccessKeyFile, when set, into SecretAccessKey.
func (c *BlobS3Config) loadSecretFile(read func(string) ([]byte, error)) error {
	if c.SecretAccessKeyFile == "" {
		return nil
	}
	raw, err := read(c.SecretAccessKeyFile)
	if err != nil {
		return fmt.Errorf("reading blob.s3.secretAccessKeyFile %q: %w", c.SecretAccessKeyFile, err)
	}
	c.SecretAccessKey = Secret(strings.TrimSpace(string(raw)))
	return nil
}
