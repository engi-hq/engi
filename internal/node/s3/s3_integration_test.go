//go:build integration

package s3_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/engi-hq/engi/internal/adapter/s3pool"
	"github.com/engi-hq/engi/internal/node/nodetest"
	s3node "github.com/engi-hq/engi/internal/node/s3"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// The official minio/minio images are no longer published; Chainguard ships a maintained build.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

const minioImage = "cgr.dev/chainguard/minio:latest"

// memBlob is an in-memory BlobAccess.
type memBlob struct{ data map[string][]byte }

func (m *memBlob) Put(_ context.Context, name, mime string, r io.Reader) (nodesdk.BinaryRef, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nodesdk.BinaryRef{}, err
	}
	ref := "blob-" + name
	m.data[ref] = b
	return nodesdk.BinaryRef{Ref: ref, MIME: mime, Name: name, Size: int64(len(b))}, nil
}

func (m *memBlob) Open(_ context.Context, ref nodesdk.BinaryRef) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data[ref.Ref])), nil
}

type blobInput struct {
	*nodetest.Input
	blob *memBlob
}

func (b blobInput) Blob() nodesdk.BlobAccess { return b.blob }

func TestMinIO(t *testing.T) {
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
	endpoint := "http://" + addr
	pool := s3pool.New(s3pool.Config{ConnectTimeout: 10 * time.Second}, quiet)
	t.Cleanup(func() { _ = pool.Close() })
	cred := credential(endpoint, 1)
	cred.Data["accessKeyId"], cred.Data["secretAccessKey"] = c.Username, c.Password
	raw, err := pool.Client(ctx, cred)
	if err != nil {
		t.Fatal(err)
	}
	bucket := "engi-test"
	if _, err := raw.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &bucket}); err != nil {
		t.Fatalf("creating bucket: %v", err)
	}

	node := s3node.New(pool, s3node.Limits{MaxKeys: 3, MaxObjectBytes: 64, MaxUploadBytes: 1024, PresignMaxExpiry: time.Hour, RequestTimeout: 30 * time.Second})
	blob := &memBlob{data: map[string][]byte{}}
	do := func(t *testing.T, item nodesdk.Item, params map[string]any) []nodesdk.Item {
		t.Helper()
		in := nodetest.New(node.Describe(), []nodesdk.Item{item}, params)
		in.Credentials = map[string]nodesdk.Credential{"engi.s3": cred}
		out, err := node.Execute(ctx, blobInput{in, blob})
		if err != nil {
			t.Fatalf("%v: %v", params["operation"], err)
		}
		return out.Ports[0]
	}
	p := func(kv ...any) map[string]any {
		m := map[string]any{"bucket": bucket}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	t.Run("list buckets", func(t *testing.T) {
		out := do(t, nodesdk.Item{}, p("operation", "listBuckets"))
		if len(out) != 1 || out[0].JSON["name"] != bucket {
			t.Fatalf("buckets = %v", out)
		}
	})
	t.Run("put text, json and binary then get", func(t *testing.T) {
		do(t, nodesdk.Item{}, p("operation", "put", "key", "docs/a.txt", "source", "text", "value", "hello"))
		do(t, nodesdk.Item{}, p("operation", "put", "key", "docs/b.json", "source", "json", "value", map[string]any{"n": 1.0}))
		blob.data["r1"] = []byte("\x00\x01binary")
		bin := nodesdk.Item{Binary: map[string]nodesdk.BinaryRef{"data": {Ref: "r1", MIME: "application/octet-stream"}}}
		do(t, bin, p("operation", "put", "key", "docs/c.bin", "source", "binary"))

		if got := do(t, nodesdk.Item{}, p("operation", "get", "key", "docs/a.txt", "as", "text"))[0].JSON["data"]; got != "hello" {
			t.Errorf("text = %v", got)
		}
		j, _ := do(t, nodesdk.Item{}, p("operation", "get", "key", "docs/b.json", "as", "json"))[0].JSON["data"].(map[string]any)
		if j["n"] != 1.0 {
			t.Errorf("json = %v", j)
		}
		it := do(t, nodesdk.Item{}, p("operation", "get", "key", "docs/c.bin", "as", "binary", "binaryField", "file"))[0]
		if string(blob.data[it.Binary["file"].Ref]) != "\x00\x01binary" {
			t.Errorf("binary = %q", blob.data[it.Binary["file"].Ref])
		}
	})
	t.Run("get over the in-item cap fails", func(t *testing.T) {
		do(t, nodesdk.Item{}, p("operation", "put", "key", "big.txt", "source", "text", "value", strings.Repeat("x", 65)))
		in := nodetest.New(node.Describe(), []nodesdk.Item{{}}, p("operation", "get", "key", "big.txt", "as", "text"))
		in.Credentials = map[string]nodesdk.Credential{"engi.s3": cred}
		if _, err := node.Execute(ctx, in); err == nil || !strings.Contains(err.Error(), "maxObjectBytes") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("list with prefix, delimiter and cap", func(t *testing.T) {
		out := do(t, nodesdk.Item{}, p("operation", "list", "prefix", "docs/"))
		if len(out) != 3 {
			t.Fatalf("objects = %d, want 3", len(out))
		}
		out = do(t, nodesdk.Item{}, p("operation", "list", "delimiter", "/"))
		var prefixes int
		for _, it := range out {
			if it.JSON["prefix"] == "docs/" {
				prefixes++
			}
		}
		if prefixes != 1 {
			t.Fatalf("prefix items = %d in %v", prefixes, out)
		}
		out = do(t, nodesdk.Item{}, p("operation", "list", "maxKeys", 100.0))
		if len(out) != 3 { // capped by Limits.MaxKeys = 3 even though 4 objects exist
			t.Fatalf("capped list = %d, want 3", len(out))
		}
	})
	t.Run("copy and delete", func(t *testing.T) {
		do(t, nodesdk.Item{}, p("operation", "copy", "key", "docs/a.txt", "destKey", "copy/a b.txt"))
		if got := do(t, nodesdk.Item{}, p("operation", "get", "key", "copy/a b.txt"))[0].JSON["data"]; got != "hello" {
			t.Errorf("copied = %v", got)
		}
		do(t, nodesdk.Item{}, p("operation", "delete", "key", "copy/a b.txt"))
		in := nodetest.New(node.Describe(), []nodesdk.Item{{}}, p("operation", "get", "key", "copy/a b.txt"))
		in.Credentials = map[string]nodesdk.Credential{"engi.s3": cred}
		if _, err := node.Execute(ctx, in); err == nil {
			t.Error("deleted object must not be readable")
		}
	})
	t.Run("presigned download works", func(t *testing.T) {
		u, _ := do(t, nodesdk.Item{}, p("operation", "presign", "key", "docs/a.txt", "expiresIn", 60.0))[0].JSON["url"].(string)
		resp, err := nodeHTTPGet(ctx, u)
		if err != nil || resp != "hello" {
			t.Fatalf("GET presigned = %q, %v", resp, err)
		}
	})
}

func nodeHTTPGet(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return string(b), err
}
