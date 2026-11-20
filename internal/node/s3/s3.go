// Package s3 implements the S3-compatible storage node (engi.storage.s3).
package s3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	creds3 "github.com/engi-hq/engi/internal/credtype/s3"
	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.storage.s3"

// Operations.
const (
	opListBuckets = "listBuckets"
	opList        = "list"
	opGet         = "get"
	opPut         = "put"
	opDelete      = "delete"
	opCopy        = "copy"
	opPresign     = "presign"
)

// Body formats of get (as) and put (source).
const (
	formText   = "text"
	formJSON   = "json"
	formBinary = "binary"
)

// Presigned URL methods.
const (
	methodGet = "get"
	methodPut = "put"
)

// Parameter defaults that are not host limits.
const (
	defaultBinaryField   = "data"
	defaultPresignExpiry = 900 // seconds
)

// Clients returns the shared S3 client of a credential (satisfied by
// s3pool.Manager).
type Clients interface {
	Client(ctx context.Context, cred nodesdk.Credential) (*awss3.Client, error)
}

// Limits are the host settings of the node (nodes.s3.*).
type Limits struct {
	// MaxKeys caps the objects one list returns.
	MaxKeys int
	// MaxObjectBytes caps an object read into an item as text or JSON.
	MaxObjectBytes int64
	// MaxUploadBytes caps an upload; the body is buffered so it can be signed.
	MaxUploadBytes int64
	// PresignMaxExpiry caps the lifetime of a presigned URL.
	PresignMaxExpiry time.Duration
	// RequestTimeout bounds one S3 request, per item.
	RequestTimeout time.Duration
}

// Node is the S3 node.
type Node struct {
	clients Clients
	lim     Limits
}

// New returns the node. Without clients Execute fails with
// nodesdk.ErrUnavailable.
func New(clients Clients, lim Limits) *Node { return &Node{clients: clients, lim: lim} }

// Describe implements nodesdk.Node.
func (*Node) Describe() nodesdk.Descriptor {
	op := func(v string) *nodesdk.DisplayOptions { return nodekit.Show("operation", v) }
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "S3 Storage", Group: nodesdk.GroupAction, Icon: "database",
		Description: "Work with buckets and objects in AWS S3 or any S3-compatible store (MinIO, R2, ...).",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Credentials: []nodesdk.CredentialRef{{Name: creds3.Name, Type: creds3.Name, Required: true}},
		Properties: []nodesdk.Property{
			{Name: "operation", DisplayName: "Operation", Type: nodesdk.PropOptions, Default: opGet, Options: []nodesdk.Option{
				nodekit.Opt(opListBuckets, "List buckets"), nodekit.Opt(opList, "List objects"), nodekit.Opt(opGet, "Get object"),
				nodekit.Opt(opPut, "Put object"), nodekit.Opt(opDelete, "Delete object"), nodekit.Opt(opCopy, "Copy object"),
				nodekit.Opt(opPresign, "Presigned URL")}},
			{Name: "bucket", DisplayName: "Bucket", Type: nodesdk.PropString, Required: true,
				DisplayOptions: &nodesdk.DisplayOptions{Hide: map[string][]any{"operation": {opListBuckets}}}},
			{Name: "key", DisplayName: "Key", Type: nodesdk.PropString, Required: true,
				DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{"operation": {opGet, opPut, opDelete, opCopy, opPresign}}}},
			{Name: "prefix", DisplayName: "Prefix", Type: nodesdk.PropString, DisplayOptions: op(opList)},
			{Name: "delimiter", DisplayName: "Delimiter", Type: nodesdk.PropString, DisplayOptions: op(opList),
				Description: "Usually \"/\": groups keys below a common prefix into one prefix item."},
			{Name: "maxKeys", DisplayName: "Max objects", Type: nodesdk.PropNumber, DisplayOptions: op(opList),
				Description: "Capped by nodes.s3.maxKeys; empty uses the cap."},
			{Name: "as", DisplayName: "Return as", Type: nodesdk.PropOptions, Default: formText, DisplayOptions: op(opGet), Options: []nodesdk.Option{
				nodekit.Opt(formText, "Text in the item"), nodekit.Opt(formJSON, "JSON in the item"), nodekit.Opt(formBinary, "Binary data")}},
			{Name: "source", DisplayName: "Body from", Type: nodesdk.PropOptions, Default: formText, DisplayOptions: op(opPut), Options: []nodesdk.Option{
				nodekit.Opt(formText, "Text"), nodekit.Opt(formJSON, "JSON"), nodekit.Opt(formBinary, "Binary data")}},
			{Name: "value", DisplayName: "Body", Type: nodesdk.PropString, DisplayOptions: &nodesdk.DisplayOptions{
				Show: map[string][]any{"operation": {opPut}, "source": {formText, formJSON}}}},
			{Name: "binaryField", DisplayName: "Binary field", Type: nodesdk.PropString, Default: defaultBinaryField,
				Description:    "Binary property of the item to upload, or to store the downloaded object in.",
				DisplayOptions: &nodesdk.DisplayOptions{Show: map[string][]any{"operation": {opGet, opPut}}}},
			{Name: "contentType", DisplayName: "Content type", Type: nodesdk.PropString, DisplayOptions: op(opPut),
				Description: "Empty uses application/json, text/plain or the binary's MIME type."},
			{Name: "destBucket", DisplayName: "Destination bucket", Type: nodesdk.PropString, DisplayOptions: op(opCopy),
				Description: "Empty copies within the source bucket."},
			{Name: "destKey", DisplayName: "Destination key", Type: nodesdk.PropString, Required: true, DisplayOptions: op(opCopy)},
			{Name: "method", DisplayName: "URL for", Type: nodesdk.PropOptions, Default: methodGet, DisplayOptions: op(opPresign), Options: []nodesdk.Option{
				nodekit.Opt(methodGet, "Download"), nodekit.Opt(methodPut, "Upload")}},
			{Name: "expiresIn", DisplayName: "Expires in (seconds)", Type: nodesdk.PropNumber, Default: defaultPresignExpiry, DisplayOptions: op(opPresign),
				Description: "Capped by nodes.s3.presignMaxExpiry."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n *Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	if len(items) == 0 {
		return nodesdk.Output{Ports: [][]nodesdk.Item{nil}}, nil
	}
	if n.clients == nil {
		return nodesdk.Output{}, fmt.Errorf("s3 clients: %w", nodesdk.ErrUnavailable)
	}
	cred, err := in.Credential(ctx, creds3.Name)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("loading credential %q: %w", creds3.Name, err)
	}
	cl, err := n.clients.Client(ctx, cred)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("opening s3 client: %w", err)
	}
	var out []nodesdk.Item
	for i := range items {
		if err := ctx.Err(); err != nil {
			return nodesdk.Output{}, fmt.Errorf("s3 node cancelled: %w", err)
		}
		res, err := n.runItem(ctx, in, cl, i, items[i])
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		for _, it := range res {
			out = append(out, nodekit.Paired(it, i))
		}
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func (n *Node) runItem(ctx context.Context, in nodesdk.Input, cl *awss3.Client, i int, item nodesdk.Item) ([]nodesdk.Item, error) {
	op, err := nodesdk.ParamString(ctx, in, "operation", i)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, n.lim.RequestTimeout)
	defer cancel()
	if op == opListBuckets {
		return n.listBuckets(rctx, cl)
	}
	bucket, err := nodesdk.ParamString(ctx, in, "bucket", i)
	if err != nil {
		return nil, err
	}
	if bucket == "" {
		return nil, errors.New("bucket must not be empty")
	}
	if op == opList {
		return n.list(rctx, in, cl, i, bucket)
	}
	key, err := nodesdk.ParamString(ctx, in, "key", i)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("key must not be empty")
	}
	switch op {
	case opGet:
		return n.get(rctx, in, cl, i, bucket, key)
	case opPut:
		return n.put(rctx, in, cl, i, item, bucket, key)
	case opDelete:
		if _, err := cl.DeleteObject(rctx, &awss3.DeleteObjectInput{Bucket: &bucket, Key: &key}); err != nil {
			return nil, fmt.Errorf("deleting %s/%s: %w", bucket, key, err)
		}
		return one(map[string]any{"bucket": bucket, "key": key, "deleted": true}), nil
	case opCopy:
		return n.copyObject(rctx, in, cl, i, bucket, key)
	case opPresign:
		return n.presign(ctx, in, cl, i, bucket, key)
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

func one(m map[string]any) []nodesdk.Item { return []nodesdk.Item{{JSON: m}} }

// copySource builds the x-amz-copy-source value: bucket/key, key escaped per segment.
func copySource(bucket, key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return bucket + "/" + strings.Join(segs, "/")
}
