package app

import (
	"context"
	"fmt"

	blobfs "github.com/engi-hq/engi/internal/adapter/blob/fs"
	blobs3 "github.com/engi-hq/engi/internal/adapter/blob/s3"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/blob"
)

// buildBlobs builds the binary data service on the configured driver.
func buildBlobs(ctx context.Context, cfg config.BlobConfig) (*blob.Service, error) {
	var store port.BlobStore
	switch cfg.Driver {
	case config.BlobDriverFS:
		store = blobfs.New(cfg.FS.Dir)
	case config.BlobDriverS3:
		s, err := blobs3.New(ctx, cfg.S3)
		if err != nil {
			return nil, fmt.Errorf("building s3 blob store: %w", err)
		}
		store = s
	default:
		return nil, fmt.Errorf("unsupported blob driver %q", cfg.Driver)
	}
	return blob.New(cfg, store), nil
}
