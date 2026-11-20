// Package s3pool builds and caches S3 clients, one per credential version.
package s3pool

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/engi-hq/engi/internal/clientcache"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Config bounds every client. See config.S3NodeConfig.
type Config struct {
	ConnectTimeout time.Duration
}

// Manager is safe for concurrent use.
type Manager struct {
	cfg   Config
	cache *clientcache.Cache[*s3.Client]
}

// New returns a manager with no clients open.
func New(cfg Config, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, cache: clientcache.New(log, func(*s3.Client) error { return nil })}
}

// Client returns the shared client for the credential.
func (m *Manager) Client(ctx context.Context, cred nodesdk.Credential) (*s3.Client, error) {
	return m.cache.Get(ctx, "client", cred, func() (*s3.Client, error) { return m.newClient(cred.Data) })
}

// Len reports the number of live clients.
func (m *Manager) Len() int { return m.cache.Len() }

// Close drops every client and rejects further requests.
func (m *Manager) Close() error { return m.cache.Close() }

func (m *Manager) newClient(data map[string]any) (*s3.Client, error) {
	region, id := str(data, "region"), str(data, "accessKeyId")
	secret, _ := data["secretAccessKey"].(string)
	token, _ := data["sessionToken"].(string)
	if region == "" || id == "" || secret == "" {
		return nil, errors.New("s3 credential needs region, accessKeyId and secretAccessKey")
	}
	skip, _ := data["tlsSkipVerify"].(bool)
	pathStyle, _ := data["forcePathStyle"].(bool)
	httpClient := awshttp.NewBuildableClient().
		WithDialerOptions(func(d *net.Dialer) { d.Timeout = m.cfg.ConnectTimeout }).
		WithTransportOptions(func(t *http.Transport) {
			t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: skip} //nolint:gosec // explicit development-only credential option
		})
	opts := s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(id, secret, token),
		UsePathStyle: pathStyle,
		HTTPClient:   httpClient,
		// Only checksum when the operation demands it: some S3-compatible stores reject the default trailing checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if ep := str(data, "endpoint"); ep != "" {
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			return nil, fmt.Errorf("endpoint %q must start with http:// or https://", ep)
		}
		opts.BaseEndpoint = aws.String(ep)
	}
	return s3.New(opts), nil
}

func str(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return strings.TrimSpace(s)
}
