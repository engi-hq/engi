package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// HTTPClientConfig tunes the outbound HTTP client shared by nodes.
type HTTPClientConfig struct {
	// Timeout bounds one request including reading the response body. A node
	// may set a shorter or longer deadline on the request context.
	Timeout time.Duration `yaml:"timeout"`
	// DialTimeout bounds establishing the TCP connection.
	DialTimeout time.Duration `yaml:"dialTimeout"`
	// TLSHandshakeTimeout bounds the TLS handshake.
	TLSHandshakeTimeout time.Duration `yaml:"tlsHandshakeTimeout"`
	// IdleConnTimeout closes pooled connections idle for this long.
	IdleConnTimeout time.Duration `yaml:"idleConnTimeout"`
	// MaxIdleConns caps the idle connection pool.
	MaxIdleConns int `yaml:"maxIdleConns"`
	// MaxRedirects caps the redirects followed for one request.
	MaxRedirects int `yaml:"maxRedirects"`
	// MaxResponseBytes caps the response body a node may read.
	MaxResponseBytes int64 `yaml:"maxResponseBytes"`
	// ProxyURL routes all requests through this proxy. Empty means no proxy.
	ProxyURL string `yaml:"proxyUrl"`
	// TLSInsecureSkipVerify disables server certificate verification. Only for
	// development against self-signed endpoints.
	TLSInsecureSkipVerify bool `yaml:"tlsInsecureSkipVerify"`
	// TLSCAFile is a PEM bundle of extra trusted root certificates.
	TLSCAFile string `yaml:"tlsCaFile"`
	// UserAgent is sent unless a node sets its own User-Agent header.
	UserAgent string `yaml:"userAgent"`
}

func defaultHTTPClient() HTTPClientConfig {
	return HTTPClientConfig{
		Timeout:             30 * time.Second,
		DialTimeout:         10 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConns:        100,
		MaxRedirects:        10,
		MaxResponseBytes:    10 << 20,
		UserAgent:           "engi",
	}
}

// Validate reports every problem with the HTTP client settings.
func (c HTTPClientConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "httpClient.timeout", int64(c.Timeout))
	errs = appendPositive(errs, "httpClient.dialTimeout", int64(c.DialTimeout))
	errs = appendPositive(errs, "httpClient.tlsHandshakeTimeout", int64(c.TLSHandshakeTimeout))
	errs = appendPositive(errs, "httpClient.idleConnTimeout", int64(c.IdleConnTimeout))
	errs = appendPositive(errs, "httpClient.maxResponseBytes", c.MaxResponseBytes)
	if c.MaxIdleConns < 0 {
		errs = append(errs, errors.New("httpClient.maxIdleConns must not be negative"))
	}
	if c.MaxRedirects < 0 {
		errs = append(errs, errors.New("httpClient.maxRedirects must not be negative"))
	}
	if c.ProxyURL != "" {
		if u, err := url.Parse(c.ProxyURL); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("httpClient.proxyUrl %q is not an absolute URL", c.ProxyURL))
		}
	}
	return errors.Join(errs...)
}

func durationSetting(dst func(*Config) *time.Duration) func(*Config, string) error {
	return func(c *Config, v string) (err error) { *dst(c), err = time.ParseDuration(v); return }
}

func intSetting(dst func(*Config) *int) func(*Config, string) error {
	return func(c *Config, v string) (err error) { *dst(c), err = strconv.Atoi(v); return }
}

func boolSetting(dst func(*Config) *bool) func(*Config, string) error {
	return func(c *Config, v string) (err error) { *dst(c), err = strconv.ParseBool(v); return }
}

func stringSetting(dst func(*Config) *string) func(*Config, string) error {
	return func(c *Config, v string) error { *dst(c) = v; return nil }
}

// httpClientSettings are the env/flag bindings of the httpClient section.
func httpClientSettings() []setting {
	return []setting{
		{"ENGI_HTTP_CLIENT_TIMEOUT", "http-client-timeout", "outbound request timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.HTTPClient.Timeout })},
		{"ENGI_HTTP_CLIENT_DIAL_TIMEOUT", "http-client-dial-timeout", "outbound connect timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.HTTPClient.DialTimeout })},
		{"ENGI_HTTP_CLIENT_TLS_HANDSHAKE_TIMEOUT", "http-client-tls-handshake-timeout", "outbound TLS handshake timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.HTTPClient.TLSHandshakeTimeout })},
		{"ENGI_HTTP_CLIENT_IDLE_CONN_TIMEOUT", "http-client-idle-conn-timeout", "idle connection lifetime",
			durationSetting(func(c *Config) *time.Duration { return &c.HTTPClient.IdleConnTimeout })},
		{"ENGI_HTTP_CLIENT_MAX_IDLE_CONNS", "http-client-max-idle-conns", "idle connection pool size",
			intSetting(func(c *Config) *int { return &c.HTTPClient.MaxIdleConns })},
		{"ENGI_HTTP_CLIENT_MAX_REDIRECTS", "http-client-max-redirects", "max redirects followed per request",
			intSetting(func(c *Config) *int { return &c.HTTPClient.MaxRedirects })},
		{"ENGI_HTTP_CLIENT_MAX_RESPONSE_BYTES", "http-client-max-response-bytes", "max response body bytes",
			func(c *Config, v string) (err error) {
				c.HTTPClient.MaxResponseBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_HTTP_CLIENT_PROXY_URL", "http-client-proxy-url", "outbound proxy URL",
			stringSetting(func(c *Config) *string { return &c.HTTPClient.ProxyURL })},
		{"ENGI_HTTP_CLIENT_TLS_INSECURE_SKIP_VERIFY", "http-client-tls-insecure-skip-verify", "skip TLS certificate verification",
			boolSetting(func(c *Config) *bool { return &c.HTTPClient.TLSInsecureSkipVerify })},
		{"ENGI_HTTP_CLIENT_TLS_CA_FILE", "http-client-tls-ca-file", "PEM bundle of extra trusted CAs",
			stringSetting(func(c *Config) *string { return &c.HTTPClient.TLSCAFile })},
		{"ENGI_HTTP_CLIENT_USER_AGENT", "http-client-user-agent", "default User-Agent header",
			stringSetting(func(c *Config) *string { return &c.HTTPClient.UserAgent })},
	}
}
