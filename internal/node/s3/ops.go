package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func (n *Node) listBuckets(ctx context.Context, cl *awss3.Client) ([]nodesdk.Item, error) {
	res, err := cl.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing buckets: %w", err)
	}
	out := make([]nodesdk.Item, 0, len(res.Buckets))
	for _, b := range res.Buckets {
		m := map[string]any{"name": aws.ToString(b.Name)}
		if b.CreationDate != nil {
			m["creationDate"] = b.CreationDate.UTC().Format(time.RFC3339)
		}
		out = append(out, nodesdk.Item{JSON: m})
	}
	return out, nil
}

// list returns one item per object, then one per common prefix, at most
// maxKeys objects in all.
func (n *Node) list(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, bucket string) ([]nodesdk.Item, error) {
	prefix, err := nodesdk.ParamString(ctx, in, "prefix", i)
	if err != nil {
		return nil, err
	}
	delim, err := nodesdk.ParamString(ctx, in, "delimiter", i)
	if err != nil {
		return nil, err
	}
	limit := n.lim.MaxKeys
	if v, err := in.Param(ctx, "maxKeys", i); err != nil {
		return nil, err
	} else if v != nil && v != "" {
		f, err := nodekit.ParamFloat(ctx, in, "maxKeys", i)
		if err != nil {
			return nil, err
		}
		if f < 1 {
			return nil, errors.New("maxKeys must be at least 1")
		}
		limit = min(int(f), n.lim.MaxKeys)
	}
	input := &awss3.ListObjectsV2Input{Bucket: &bucket}
	if prefix != "" {
		input.Prefix = &prefix
	}
	if delim != "" {
		input.Delimiter = &delim
	}
	var objects, prefixes []nodesdk.Item
	pager := awss3.NewListObjectsV2Paginator(cl, input)
	for pager.HasMorePages() && len(objects) < limit {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", bucket, err)
		}
		for _, o := range page.Contents {
			if len(objects) == limit {
				break
			}
			m := map[string]any{"key": aws.ToString(o.Key), "size": float64(aws.ToInt64(o.Size)), "etag": aws.ToString(o.ETag), "storageClass": string(o.StorageClass)}
			if o.LastModified != nil {
				m["lastModified"] = o.LastModified.UTC().Format(time.RFC3339)
			}
			objects = append(objects, nodesdk.Item{JSON: m})
		}
		for _, p := range page.CommonPrefixes {
			prefixes = append(prefixes, nodesdk.Item{JSON: map[string]any{"prefix": aws.ToString(p.Prefix)}})
		}
	}
	return append(objects, prefixes...), nil
}

func (n *Node) get(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, bucket, key string) ([]nodesdk.Item, error) {
	as, err := nodesdk.ParamString(ctx, in, "as", i)
	if err != nil {
		return nil, err
	}
	switch as {
	case formText, formJSON, formBinary:
	default:
		return nil, fmt.Errorf("as %q is not text, json or binary", as)
	}
	res, err := cl.GetObject(ctx, &awss3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("getting %s/%s: %w", bucket, key, err)
	}
	defer res.Body.Close()
	size, mime := aws.ToInt64(res.ContentLength), aws.ToString(res.ContentType)
	meta := map[string]any{"bucket": bucket, "key": key, "size": float64(size), "contentType": mime, "etag": aws.ToString(res.ETag)}
	if res.LastModified != nil {
		meta["lastModified"] = res.LastModified.UTC().Format(time.RFC3339)
	}
	if as == formBinary {
		field, err := nodesdk.ParamString(ctx, in, "binaryField", i)
		if err != nil {
			return nil, err
		}
		if field == "" {
			field = defaultBinaryField
		}
		ref, err := in.Blob().Put(ctx, key, mime, res.Body)
		if err != nil {
			return nil, fmt.Errorf("storing %s/%s as binary: %w", bucket, key, err)
		}
		return []nodesdk.Item{{JSON: meta, Binary: map[string]nodesdk.BinaryRef{field: ref}}}, nil
	}
	if size > n.lim.MaxObjectBytes {
		return nil, fmt.Errorf("object is %d bytes; at most %d fit in an item (nodes.s3.maxObjectBytes); return it as binary instead", size, n.lim.MaxObjectBytes)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, n.lim.MaxObjectBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s/%s: %w", bucket, key, err)
	}
	if int64(len(body)) > n.lim.MaxObjectBytes {
		return nil, fmt.Errorf("object is larger than %d bytes (nodes.s3.maxObjectBytes); return it as binary instead", n.lim.MaxObjectBytes)
	}
	if as == formJSON {
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("%s/%s is not valid JSON: %w", bucket, key, err)
		}
		meta["data"] = v
	} else {
		meta["data"] = string(body)
	}
	return one(meta), nil
}

func (n *Node) put(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, item nodesdk.Item, bucket, key string) ([]nodesdk.Item, error) {
	body, mime, err := n.uploadBody(ctx, in, i, item)
	if err != nil {
		return nil, err
	}
	ct, err := nodesdk.ParamString(ctx, in, "contentType", i)
	if err != nil {
		return nil, err
	}
	if ct == "" {
		ct = mime
	}
	input := &awss3.PutObjectInput{Bucket: &bucket, Key: &key, Body: bytes.NewReader(body)}
	if ct != "" {
		input.ContentType = &ct
	}
	res, err := cl.PutObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("putting %s/%s: %w", bucket, key, err)
	}
	return one(map[string]any{"bucket": bucket, "key": key, "size": float64(len(body)), "etag": aws.ToString(res.ETag)}), nil
}

// uploadBody resolves the bytes and default MIME type of a put.
func (n *Node) uploadBody(ctx context.Context, in nodesdk.Input, i int, item nodesdk.Item) (body []byte, mime string, err error) {
	source, err := nodesdk.ParamString(ctx, in, "source", i)
	if err != nil {
		return nil, "", err
	}
	switch source {
	case formText, formJSON:
		v, err := in.Param(ctx, "value", i)
		if err != nil {
			return nil, "", err
		}
		if s, ok := v.(string); ok && source == formText {
			body, mime = []byte(s), "text/plain"
			break
		}
		if body, err = json.Marshal(v); err != nil {
			return nil, "", fmt.Errorf("encoding body as JSON: %w", err)
		}
		mime = "application/json"
	case formBinary:
		field, err := nodesdk.ParamString(ctx, in, "binaryField", i)
		if err != nil {
			return nil, "", err
		}
		if field == "" {
			field = defaultBinaryField
		}
		ref, ok := item.Binary[field]
		if !ok {
			return nil, "", fmt.Errorf("item has no binary field %q", field)
		}
		rc, err := in.Blob().Open(ctx, ref)
		if err != nil {
			return nil, "", fmt.Errorf("opening binary %q: %w", field, err)
		}
		defer rc.Close()
		if body, err = io.ReadAll(io.LimitReader(rc, n.lim.MaxUploadBytes+1)); err != nil {
			return nil, "", fmt.Errorf("reading binary %q: %w", field, err)
		}
		mime = ref.MIME
	default:
		return nil, "", fmt.Errorf("source %q is not text, json or binary", source)
	}
	if int64(len(body)) > n.lim.MaxUploadBytes {
		return nil, "", fmt.Errorf("body is larger than %d bytes (nodes.s3.maxUploadBytes)", n.lim.MaxUploadBytes)
	}
	return body, mime, nil
}

func (n *Node) copyObject(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, bucket, key string) ([]nodesdk.Item, error) {
	destBucket, err := nodesdk.ParamString(ctx, in, "destBucket", i)
	if err != nil {
		return nil, err
	}
	if destBucket == "" {
		destBucket = bucket
	}
	destKey, err := nodesdk.ParamString(ctx, in, "destKey", i)
	if err != nil {
		return nil, err
	}
	if destKey == "" {
		return nil, errors.New("destKey must not be empty")
	}
	src := copySource(bucket, key)
	res, err := cl.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: &destBucket, Key: &destKey, CopySource: &src})
	if err != nil {
		return nil, fmt.Errorf("copying %s/%s to %s/%s: %w", bucket, key, destBucket, destKey, err)
	}
	m := map[string]any{"bucket": destBucket, "key": destKey, "sourceBucket": bucket, "sourceKey": key}
	if res.CopyObjectResult != nil {
		m["etag"] = aws.ToString(res.CopyObjectResult.ETag)
	}
	return one(m), nil
}

func (n *Node) presign(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, bucket, key string) ([]nodesdk.Item, error) {
	method, err := nodesdk.ParamString(ctx, in, "method", i)
	if err != nil {
		return nil, err
	}
	secs, err := nodekit.ParamFloat(ctx, in, "expiresIn", i)
	if err != nil {
		return nil, err
	}
	expiry := time.Duration(secs) * time.Second
	if expiry <= 0 {
		return nil, errors.New("expiresIn must be positive")
	}
	if expiry > n.lim.PresignMaxExpiry {
		return nil, fmt.Errorf("expiresIn %s exceeds the limit of %s (nodes.s3.presignMaxExpiry)", expiry, n.lim.PresignMaxExpiry)
	}
	ps := awss3.NewPresignClient(cl, awss3.WithPresignExpires(expiry))
	var urlStr string
	switch method {
	case methodGet:
		r, err := ps.PresignGetObject(ctx, &awss3.GetObjectInput{Bucket: &bucket, Key: &key})
		if err != nil {
			return nil, fmt.Errorf("presigning %s/%s: %w", bucket, key, err)
		}
		urlStr = r.URL
	case methodPut:
		r, err := ps.PresignPutObject(ctx, &awss3.PutObjectInput{Bucket: &bucket, Key: &key})
		if err != nil {
			return nil, fmt.Errorf("presigning %s/%s: %w", bucket, key, err)
		}
		urlStr = r.URL
	default:
		return nil, fmt.Errorf("method %q is not get or put", method)
	}
	return one(map[string]any{"bucket": bucket, "key": key, "method": method, "url": urlStr, "expiresIn": secs}), nil
}
