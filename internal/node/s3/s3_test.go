package s3_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	creds3 "github.com/engi-hq/engi/internal/credtype/s3"

	"github.com/engi-hq/engi/internal/node/nodetest"
	s3node "github.com/engi-hq/engi/internal/node/s3"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

var testLimits = s3node.Limits{MaxKeys: 100, MaxObjectBytes: 16, MaxUploadBytes: 16, PresignMaxExpiry: time.Hour, RequestTimeout: 5 * time.Second}

func credential(endpoint string, version int64) nodesdk.Credential {
	return nodesdk.Credential{Type: creds3.Name, ID: "c1", Version: version, Data: map[string]any{
		"endpoint": endpoint, "region": "us-east-1", "accessKeyId": "AKID", "secretAccessKey": "secret", "forcePathStyle": true,
	}}
}

func run(t *testing.T, n *s3node.Node, endpoint string, params map[string]any) (nodesdk.Output, error) {
	t.Helper()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), params)
	in.Credentials = map[string]nodesdk.Credential{creds3.Name: credential(endpoint, 1)}
	return n.Execute(context.Background(), in)
}

// staticClients builds a client straight from the credential endpoint.
type staticClients struct{}

func (staticClients) Client(_ context.Context, cred nodesdk.Credential) (*awss3.Client, error) {
	return awss3.New(awss3.Options{
		Region: "us-east-1", BaseEndpoint: aws.String(cred.Data["endpoint"].(string)), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "secret", ""),
	}), nil
}

func newNode(t *testing.T) *s3node.Node {
	t.Helper()
	return s3node.New(staticClients{}, testLimits)
}

func TestDescriptorIsValid(t *testing.T) {
	if err := s3node.New(nil, testLimits).Describe().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutClientsIsUnavailable(t *testing.T) {
	_, err := run(t, s3node.New(nil, testLimits), "http://localhost", map[string]any{"operation": "listBuckets"})
	if !errors.Is(err, nodesdk.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestPresign(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr string
		wantURL string
	}{
		{name: "download", params: map[string]any{"expiresIn": 60.0}, wantURL: "/bkt/dir/a%20b.txt"},
		{name: "upload", params: map[string]any{"method": "put", "expiresIn": 60.0}, wantURL: "X-Amz-Expires=60"},
		{name: "over the cap", params: map[string]any{"expiresIn": 7200.0}, wantErr: "presignMaxExpiry"},
		{name: "not positive", params: map[string]any{"expiresIn": 0.0}, wantErr: "positive"},
	}
	n := newNode(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{"operation": "presign", "bucket": "bkt", "key": "dir/a b.txt"}
			for k, v := range tc.params {
				p[k] = v
			}
			out, err := run(t, n, "http://127.0.0.1:1", p)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			u, _ := out.Ports[0][0].JSON["url"].(string)
			if !strings.Contains(u, tc.wantURL) || !strings.Contains(u, "X-Amz-Signature=") {
				t.Fatalf("url = %q, want it to contain %q and a signature", u, tc.wantURL)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	n := newNode(t)
	tests := []struct {
		name    string
		params  map[string]any
		wantErr string
	}{
		{"empty bucket", map[string]any{"operation": "list", "bucket": ""}, "bucket"},
		{"empty key", map[string]any{"operation": "delete", "bucket": "b", "key": ""}, "key"},
		{"unknown op", map[string]any{"operation": "nuke", "bucket": "b", "key": "k"}, "unknown operation"},
		{"copy without destKey", map[string]any{"operation": "copy", "bucket": "b", "key": "k", "destKey": ""}, "destKey"},
		{"bad get format", map[string]any{"operation": "get", "bucket": "b", "key": "k", "as": "xml"}, "as"},
		{"bad put source", map[string]any{"operation": "put", "bucket": "b", "key": "k", "source": "xml"}, "source"},
		{"oversized put", map[string]any{"operation": "put", "bucket": "b", "key": "k", "value": strings.Repeat("x", 17)}, "maxUploadBytes"},
		{"zero maxKeys", map[string]any{"operation": "list", "bucket": "b", "maxKeys": 0.0}, "maxKeys"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, n, "http://127.0.0.1:1", tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestGetRejectsObjectOverCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 17)))
	}))
	defer srv.Close()
	_, err := run(t, newNode(t), srv.URL, map[string]any{"operation": "get", "bucket": "b", "key": "k", "as": "text"})
	if err == nil || !strings.Contains(err.Error(), "maxObjectBytes") {
		t.Fatalf("err = %v, want maxObjectBytes", err)
	}
}
