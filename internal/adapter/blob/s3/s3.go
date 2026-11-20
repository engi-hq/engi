// Package s3 is the blob store on an S3-compatible bucket (cluster mode). It is
// configured from the server configuration, not from a user credential.
//
// Objects live at <prefix><execution>/<id>. Small values use one PutObject;
// larger ones are streamed as a multipart upload, one part buffered at a time.
package s3

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

const (
	nameMetaKey = "name"
	// deleteBatch is the S3 limit of keys per DeleteObjects call.
	deleteBatch = 1000
)

// Store implements port.BlobStore on a bucket.
type Store struct {
	client   *awss3.Client
	bucket   string
	prefix   string
	partSize int64
}

var _ port.BlobStore = (*Store)(nil)

// New builds a store from the server's blob.s3 settings. With an access key
// pair it signs with those keys; without one it uses the AWS default
// credential chain (environment, web identity token as in EKS IRSA or
// Azure/GKE workload identity federation, shared config, instance metadata).
func New(ctx context.Context, cfg config.BlobS3Config) (*Store, error) {
	creds, err := credentialsFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	opts := awss3.Options{
		Region:       cfg.Region,
		Credentials:  creds,
		UsePathStyle: cfg.ForcePathStyle,
		HTTPClient: awshttp.NewBuildableClient().
			WithDialerOptions(func(d *net.Dialer) { d.Timeout = cfg.ConnectTimeout }).
			WithTransportOptions(func(t *http.Transport) { t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12} }),
		// Only checksum when required: some S3-compatible stores reject the default trailing checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if cfg.Endpoint != "" {
		if !strings.HasPrefix(cfg.Endpoint, "http://") && !strings.HasPrefix(cfg.Endpoint, "https://") {
			return nil, fmt.Errorf("blob.s3.endpoint %q must start with http:// or https://", cfg.Endpoint)
		}
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	return &Store{client: awss3.New(opts), bucket: cfg.Bucket, prefix: cfg.Prefix, partSize: cfg.PartSize}, nil
}

func credentialsFor(ctx context.Context, cfg config.BlobS3Config) (aws.CredentialsProvider, error) {
	if cfg.AccessKeyID != "" {
		return credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey.Reveal(), ""), nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading the default AWS credential chain: %w", err)
	}
	return awsCfg.Credentials, nil
}

func (s *Store) key(ref port.BlobRef) string { return s.prefix + ref.Execution + "/" + ref.ID }

func (s *Store) execPrefix(execution string) string { return s.prefix + execution + "/" }

// Put implements port.BlobStore.
func (s *Store) Put(ctx context.Context, ref port.BlobRef, info port.BlobInfo, r io.Reader) (int64, error) {
	if err := ref.Validate(); err != nil {
		return 0, err //nolint:wrapcheck // already descriptive
	}
	first := make([]byte, s.partSize)
	n, err := io.ReadFull(r, first)
	first = first[:n]
	switch {
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return s.putSmall(ctx, ref, info, first)
	case err != nil:
		return 0, fmt.Errorf("reading blob: %w", err)
	}
	return s.putMultipart(ctx, ref, info, first, r)
}

func (s *Store) putSmall(ctx context.Context, ref port.BlobRef, info port.BlobInfo, body []byte) (int64, error) {
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: &s.bucket, Key: aws.String(s.key(ref)), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))),
		ContentType: contentType(info), Metadata: map[string]string{nameMetaKey: url.QueryEscape(info.Name)},
	})
	if err != nil {
		return 0, fmt.Errorf("uploading blob: %w", err)
	}
	return int64(len(body)), nil
}

func (s *Store) putMultipart(ctx context.Context, ref port.BlobRef, info port.BlobInfo, first []byte, r io.Reader) (total int64, err error) {
	key := aws.String(s.key(ref))
	up, err := s.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: &s.bucket, Key: key, ContentType: contentType(info), Metadata: map[string]string{nameMetaKey: url.QueryEscape(info.Name)},
	})
	if err != nil {
		return 0, fmt.Errorf("starting multipart upload: %w", err)
	}
	defer func() {
		if err != nil {
			// The upload context may be the thing that failed; abort on a fresh one.
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
			defer cancel()
			_, _ = s.client.AbortMultipartUpload(actx, &awss3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: key, UploadId: up.UploadId})
		}
	}()
	var parts []types.CompletedPart
	buf, last := first, false
	for num := int32(1); ; num++ {
		res, uerr := s.client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: &s.bucket, Key: key, UploadId: up.UploadId, PartNumber: aws.Int32(num),
			Body: bytes.NewReader(buf), ContentLength: aws.Int64(int64(len(buf))),
		})
		if uerr != nil {
			return 0, fmt.Errorf("uploading part %d: %w", num, uerr)
		}
		parts = append(parts, types.CompletedPart{ETag: res.ETag, PartNumber: aws.Int32(num)})
		total += int64(len(buf))
		if last {
			break
		}
		next := make([]byte, s.partSize)
		n, rerr := io.ReadFull(r, next)
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil && !errors.Is(rerr, io.ErrUnexpectedEOF) {
			return 0, fmt.Errorf("reading blob: %w", rerr)
		}
		buf, last = next[:n], rerr != nil
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: &s.bucket, Key: key, UploadId: up.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return 0, fmt.Errorf("completing multipart upload: %w", err)
	}
	return total, nil
}

const abortTimeout = 30 * time.Second

func contentType(info port.BlobInfo) *string {
	if info.MIME == "" {
		return nil
	}
	return aws.String(info.MIME)
}

// Get implements port.BlobStore.
func (s *Store) Get(ctx context.Context, ref port.BlobRef) (io.ReadCloser, port.BlobMeta, error) {
	if err := ref.Validate(); err != nil {
		return nil, port.BlobMeta{}, err //nolint:wrapcheck // already descriptive
	}
	res, err := s.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: &s.bucket, Key: aws.String(s.key(ref))})
	if err != nil {
		if isNotFound(err) {
			return nil, port.BlobMeta{}, fmt.Errorf("blob %s: %w", ref, domain.ErrNotFound)
		}
		return nil, port.BlobMeta{}, fmt.Errorf("downloading blob: %w", err)
	}
	name, _ := url.QueryUnescape(res.Metadata[nameMetaKey])
	meta := port.BlobMeta{BlobInfo: port.BlobInfo{Name: name, MIME: aws.ToString(res.ContentType)}, Size: aws.ToInt64(res.ContentLength)}
	if res.LastModified != nil {
		meta.CreatedAt = *res.LastModified
	}
	return res.Body, meta, nil
}

func isNotFound(err error) bool {
	var nf *types.NoSuchKey
	var apiErr smithy.APIError
	return errors.As(err, &nf) || (errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound"))
}

// Delete implements port.BlobStore.
func (s *Store) Delete(ctx context.Context, ref port.BlobRef) error {
	if err := ref.Validate(); err != nil {
		return err //nolint:wrapcheck // already descriptive
	}
	if _, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: &s.bucket, Key: aws.String(s.key(ref))}); err != nil {
		return fmt.Errorf("deleting blob %s: %w", ref, err)
	}
	return nil
}

// DeleteExecution implements port.BlobStore.
func (s *Store) DeleteExecution(ctx context.Context, execution string) error {
	if err := (port.BlobRef{Execution: execution, ID: "x"}).Validate(); err != nil {
		return err //nolint:wrapcheck // already descriptive
	}
	var batch []types.ObjectIdentifier
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		res, err := s.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
			Bucket: &s.bucket, Delete: &types.Delete{Objects: batch, Quiet: aws.Bool(true)},
		})
		batch = batch[:0]
		if err != nil {
			return fmt.Errorf("deleting blobs of execution %s: %w", execution, err)
		}
		if len(res.Errors) > 0 {
			return fmt.Errorf("deleting blobs of execution %s: %s", execution, aws.ToString(res.Errors[0].Message))
		}
		return nil
	}
	err := s.list(ctx, s.execPrefix(execution), func(o types.Object) error {
		batch = append(batch, types.ObjectIdentifier{Key: o.Key})
		if len(batch) == deleteBatch {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// ExecutionSize implements port.BlobStore.
func (s *Store) ExecutionSize(ctx context.Context, execution string) (int64, error) {
	if err := (port.BlobRef{Execution: execution, ID: "x"}).Validate(); err != nil {
		return 0, err //nolint:wrapcheck // already descriptive
	}
	var total int64
	err := s.list(ctx, s.execPrefix(execution), func(o types.Object) error { total += aws.ToInt64(o.Size); return nil })
	return total, err
}

// Executions implements port.BlobStore.
func (s *Store) Executions(ctx context.Context) ([]port.BlobScope, error) {
	idx := map[string]int{}
	var out []port.BlobScope
	err := s.list(ctx, s.prefix, func(o types.Object) error {
		exec, _, ok := strings.Cut(strings.TrimPrefix(aws.ToString(o.Key), s.prefix), "/")
		if !ok {
			return nil
		}
		i, seen := idx[exec]
		if !seen {
			i = len(out)
			idx[exec] = i
			out = append(out, port.BlobScope{Execution: exec})
		}
		if o.LastModified != nil && o.LastModified.After(out[i].Newest) {
			out[i].Newest = *o.LastModified
		}
		return nil
	})
	return out, err
}

// list pages through the objects under prefix.
func (s *Store) list(ctx context.Context, prefix string, fn func(types.Object) error) error {
	p := awss3.NewListObjectsV2Paginator(s.client, &awss3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing blobs: %w", err)
		}
		for _, o := range page.Contents {
			if err := fn(o); err != nil {
				return err
			}
		}
	}
	return nil
}
