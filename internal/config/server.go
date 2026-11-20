package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Runtime roles a process can run (docs/02 section 2.2). The runner role joins
// when the plugin protocol lands.
const (
	RoleAPI     = "api"
	RoleWebhook = "webhook"
	RoleTrigger = "trigger"
	RoleWorker  = "worker"
)

// AllRoles lists every role; standalone mode runs all of them in one process.
func AllRoles() []string { return []string{RoleAPI, RoleWebhook, RoleTrigger, RoleWorker} }

// ServerConfig configures the HTTP server of `engi serve` and the REST API.
type ServerConfig struct {
	// Addr is the listen address.
	Addr string `yaml:"addr"`
	// Roles selects what this process runs; empty means all roles.
	Roles []string `yaml:"roles"`
	// AutoMigrate applies pending database migrations when the server starts.
	AutoMigrate bool `yaml:"autoMigrate"`

	// ReadHeaderTimeout, ReadTimeout, WriteTimeout and IdleTimeout are the
	// net/http server timeouts. Streaming responses lift their own write
	// deadline.
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
	ReadTimeout       time.Duration `yaml:"readTimeout"`
	WriteTimeout      time.Duration `yaml:"writeTimeout"`
	IdleTimeout       time.Duration `yaml:"idleTimeout"`
	// RequestTimeout bounds the context of one API request (not streams).
	RequestTimeout time.Duration `yaml:"requestTimeout"`
	// ShutdownGrace is how long in-flight HTTP requests get to finish on shutdown.
	ShutdownGrace time.Duration `yaml:"shutdownGrace"`
	// MaxBodyBytes caps API request bodies.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
	// HealthTimeout bounds one readiness probe of the store and queue.
	HealthTimeout time.Duration `yaml:"healthTimeout"`
	// SSEHeartbeat is the interval of keep-alive comments on event streams.
	SSEHeartbeat time.Duration `yaml:"sseHeartbeat"`
	// TrustForwardedFor takes the client address from X-Forwarded-For (only
	// behind a proxy you control); it feeds login rate limiting and the access log.
	TrustForwardedFor bool `yaml:"trustForwardedFor"`
	// ProblemTypeBase prefixes the `type` URI of API error documents (RFC 9457).
	ProblemTypeBase string `yaml:"problemTypeBase"`
	// PublicURL is the absolute base URL clients and external services reach
	// Engi at (scheme, host and optional path, no trailing slash). Absolute
	// links Engi hands out, such as the resume URL of a Wait node, are built on it.
	PublicURL string `yaml:"publicUrl"`
	// OAuth2CompleteRedirect, when set, is where the browser is sent after an
	// OAuth2 authorization finished (the credential ID is added as the
	// credentialId query parameter). Empty answers with a JSON document.
	OAuth2CompleteRedirect string `yaml:"oauth2CompleteRedirect"`

	CORS       CORSConfig       `yaml:"cors"`
	Pagination PaginationConfig `yaml:"pagination"`
	UI         UIConfig         `yaml:"ui"`
}

// UIConfig configures the embedded editor served by the api role.
type UIConfig struct {
	// Enabled serves the editor at /. Turn it off for an API-only deployment.
	Enabled bool `yaml:"enabled"`
	// ContentSecurityPolicy overrides the editor's default policy; empty keeps the default.
	ContentSecurityPolicy string `yaml:"contentSecurityPolicy"`
}

// CORSConfig is the cross-origin allow-list. With no origins CORS is off.
type CORSConfig struct {
	AllowedOrigins   []string      `yaml:"allowedOrigins"`
	AllowedMethods   []string      `yaml:"allowedMethods"`
	AllowedHeaders   []string      `yaml:"allowedHeaders"`
	AllowCredentials bool          `yaml:"allowCredentials"`
	MaxAge           time.Duration `yaml:"maxAge"`
}

// PaginationConfig sizes API list pages.
type PaginationConfig struct {
	DefaultLimit int `yaml:"defaultLimit"`
	MaxLimit     int `yaml:"maxLimit"`
}

const (
	mebibyte = 1 << 20
	// DefaultCSRFHeader is the header cookie-authenticated mutations must carry.
	DefaultCSRFHeader = "X-CSRF-Token"
)

func defaultServer() ServerConfig {
	return ServerConfig{
		Addr:              ":8080",
		Roles:             AllRoles(),
		AutoMigrate:       true,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		RequestTimeout:    30 * time.Second,
		ShutdownGrace:     30 * time.Second,
		MaxBodyBytes:      4 * mebibyte,
		HealthTimeout:     2 * time.Second,
		SSEHeartbeat:      15 * time.Second,
		ProblemTypeBase:   "https://engi.dev/problems/",
		PublicURL:         "http://localhost:8080",
		CORS: CORSConfig{
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type", "If-Match", "Last-Event-ID", DefaultCSRFHeader},
			MaxAge:         10 * time.Minute,
		},
		Pagination: PaginationConfig{DefaultLimit: 50, MaxLimit: 200},
		UI:         UIConfig{Enabled: true},
	}
}

// HasRole reports whether the process runs the role.
func (c ServerConfig) HasRole(role string) bool {
	return len(c.Roles) == 0 || slices.Contains(c.Roles, role)
}

// Validate reports every problem with the server settings.
func (c ServerConfig) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Addr) == "" {
		errs = append(errs, errors.New("server.addr must not be empty"))
	}
	for _, r := range c.Roles {
		if !slices.Contains(AllRoles(), r) {
			errs = append(errs, fmt.Errorf("server.roles: unknown role %q (known: %s)", r, strings.Join(AllRoles(), ", ")))
		}
	}
	errs = appendPositive(errs, "server.readHeaderTimeout", int64(c.ReadHeaderTimeout))
	errs = appendPositive(errs, "server.readTimeout", int64(c.ReadTimeout))
	errs = appendPositive(errs, "server.writeTimeout", int64(c.WriteTimeout))
	errs = appendPositive(errs, "server.idleTimeout", int64(c.IdleTimeout))
	errs = appendPositive(errs, "server.requestTimeout", int64(c.RequestTimeout))
	errs = appendPositive(errs, "server.shutdownGrace", int64(c.ShutdownGrace))
	errs = appendPositive(errs, "server.maxBodyBytes", c.MaxBodyBytes)
	errs = appendPositive(errs, "server.healthTimeout", int64(c.HealthTimeout))
	errs = appendPositive(errs, "server.sseHeartbeat", int64(c.SSEHeartbeat))
	if strings.TrimSpace(c.ProblemTypeBase) == "" {
		errs = append(errs, errors.New("server.problemTypeBase must not be empty"))
	}
	if err := ValidatePublicURL(c.PublicURL); err != nil {
		errs = append(errs, fmt.Errorf("server.publicUrl: %w", err))
	}
	errs = appendPositive(errs, "server.pagination.defaultLimit", int64(c.Pagination.DefaultLimit))
	if c.Pagination.MaxLimit < c.Pagination.DefaultLimit {
		errs = append(errs, errors.New("server.pagination.maxLimit must not be smaller than server.pagination.defaultLimit"))
	}
	if slices.Contains(c.CORS.AllowedOrigins, "*") && c.CORS.AllowCredentials {
		errs = append(errs, errors.New("server.cors: allowCredentials cannot be combined with the wildcard origin"))
	}
	if c.CORS.MaxAge < 0 {
		errs = append(errs, errors.New("server.cors.maxAge must not be negative"))
	}
	return errors.Join(errs...)
}

// ValidatePublicURL checks that u is an absolute http or https URL with a host,
// no query or fragment and no trailing slash, so that paths can be appended to it.
func ValidatePublicURL(u string) error {
	parsed, err := url.Parse(u)
	switch {
	case err != nil:
		return fmt.Errorf("%q is not a valid URL: %w", u, err)
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return fmt.Errorf("%q must start with http:// or https://", u)
	case parsed.Host == "":
		return fmt.Errorf("%q has no host", u)
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return fmt.Errorf("%q must not have a query or fragment", u)
	case strings.HasSuffix(u, "/"):
		return fmt.Errorf("%q must not end with /", u)
	}
	return nil
}

// listSetting binds a comma-separated list.
func listSetting(dst func(*Config) *[]string) func(*Config, string) error {
	return func(c *Config, v string) error { *dst(c) = splitList(v); return nil }
}

// serverSettings are the env/flag bindings of the server section.
func serverSettings() []setting {
	return []setting{
		{"ENGI_SERVER_ADDR", "server-addr", "HTTP listen address", stringSetting(func(c *Config) *string { return &c.Server.Addr })},
		{"ENGI_ROLES", "roles", "comma-separated roles to run: api, webhook, trigger, worker",
			listSetting(func(c *Config) *[]string { return &c.Server.Roles })},
		{"ENGI_SERVER_AUTO_MIGRATE", "server-auto-migrate", "apply pending migrations on start",
			boolSetting(func(c *Config) *bool { return &c.Server.AutoMigrate })},
		{"ENGI_SERVER_READ_HEADER_TIMEOUT", "server-read-header-timeout", "time to read request headers",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.ReadHeaderTimeout })},
		{"ENGI_SERVER_READ_TIMEOUT", "server-read-timeout", "time to read a whole request",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.ReadTimeout })},
		{"ENGI_SERVER_WRITE_TIMEOUT", "server-write-timeout", "time to write a response",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.WriteTimeout })},
		{"ENGI_SERVER_IDLE_TIMEOUT", "server-idle-timeout", "keep-alive idle timeout",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.IdleTimeout })},
		{"ENGI_SERVER_REQUEST_TIMEOUT", "server-request-timeout", "context deadline of one API request",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.RequestTimeout })},
		{"ENGI_SERVER_SHUTDOWN_GRACE", "server-shutdown-grace", "time in-flight requests get on shutdown",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.ShutdownGrace })},
		{"ENGI_SERVER_MAX_BODY_BYTES", "server-max-body-bytes", "largest API request body",
			func(c *Config, v string) (err error) {
				c.Server.MaxBodyBytes, err = strconv.ParseInt(v, 10, 64)
				return
			}},
		{"ENGI_SERVER_HEALTH_TIMEOUT", "server-health-timeout", "timeout of one readiness probe",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.HealthTimeout })},
		{"ENGI_SERVER_SSE_HEARTBEAT", "server-sse-heartbeat", "keep-alive interval of event streams",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.SSEHeartbeat })},
		{"ENGI_SERVER_TRUST_FORWARDED_FOR", "server-trust-forwarded-for", "take the client address from X-Forwarded-For",
			boolSetting(func(c *Config) *bool { return &c.Server.TrustForwardedFor })},
		{"ENGI_SERVER_PROBLEM_TYPE_BASE", "server-problem-type-base", "prefix of API error type URIs",
			stringSetting(func(c *Config) *string { return &c.Server.ProblemTypeBase })},
		{"ENGI_SERVER_PUBLIC_URL", "server-public-url", "absolute base URL of this server (resume links, OAuth redirects)",
			stringSetting(func(c *Config) *string { return &c.Server.PublicURL })},
		{"ENGI_SERVER_OAUTH2_COMPLETE_REDIRECT", "server-oauth2-complete-redirect", "redirect target after an OAuth2 authorization",
			stringSetting(func(c *Config) *string { return &c.Server.OAuth2CompleteRedirect })},
		{"ENGI_SERVER_CORS_ALLOWED_ORIGINS", "server-cors-allowed-origins", "comma-separated allowed CORS origins",
			listSetting(func(c *Config) *[]string { return &c.Server.CORS.AllowedOrigins })},
		{"ENGI_SERVER_CORS_ALLOWED_METHODS", "server-cors-allowed-methods", "comma-separated allowed CORS methods",
			listSetting(func(c *Config) *[]string { return &c.Server.CORS.AllowedMethods })},
		{"ENGI_SERVER_CORS_ALLOWED_HEADERS", "server-cors-allowed-headers", "comma-separated allowed CORS request headers",
			listSetting(func(c *Config) *[]string { return &c.Server.CORS.AllowedHeaders })},
		{"ENGI_SERVER_CORS_ALLOW_CREDENTIALS", "server-cors-allow-credentials", "send Access-Control-Allow-Credentials",
			boolSetting(func(c *Config) *bool { return &c.Server.CORS.AllowCredentials })},
		{"ENGI_SERVER_CORS_MAX_AGE", "server-cors-max-age", "how long browsers cache a preflight",
			durationSetting(func(c *Config) *time.Duration { return &c.Server.CORS.MaxAge })},
		{"ENGI_SERVER_UI_ENABLED", "server-ui-enabled", "serve the embedded editor at /",
			boolSetting(func(c *Config) *bool { return &c.Server.UI.Enabled })},
		{"ENGI_SERVER_UI_CONTENT_SECURITY_POLICY", "server-ui-content-security-policy", "Content-Security-Policy of the editor (empty: built-in default)",
			stringSetting(func(c *Config) *string { return &c.Server.UI.ContentSecurityPolicy })},
		{"ENGI_SERVER_PAGINATION_DEFAULT_LIMIT", "server-pagination-default-limit", "default API page size",
			intSetting(func(c *Config) *int { return &c.Server.Pagination.DefaultLimit })},
		{"ENGI_SERVER_PAGINATION_MAX_LIMIT", "server-pagination-max-limit", "largest API page size",
			intSetting(func(c *Config) *int { return &c.Server.Pagination.MaxLimit })},
	}
}
