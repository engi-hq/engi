package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Supported cookie SameSite values.
const (
	SameSiteLax    = "lax"
	SameSiteStrict = "strict"
	SameSiteNone   = "none"
)

// CookieSecureMode decides the Secure attribute of the session cookie.
type CookieSecureMode string

// Supported CookieSecureMode values.
const (
	// CookieSecureAuto sets Secure when the request reached Engi over TLS, or,
	// behind a proxy trusted with server.trustForwardedFor, when the proxy says
	// X-Forwarded-Proto is https. Use it for TLS-terminating proxies.
	CookieSecureAuto CookieSecureMode = "auto"
	// CookieSecureAlways always sets Secure. Browsers then refuse the cookie on
	// plain HTTP, so only API clients that can send the session by hand need
	// something else (an API key).
	CookieSecureAlways CookieSecureMode = "always"
	// CookieSecureNever never sets Secure. For plain-HTTP development and for
	// in-cluster clients that keep the cookie themselves.
	CookieSecureNever CookieSecureMode = "never"
)

// ParseCookieSecureMode reads a mode from an env var, flag or YAML string. The
// booleans from before the mode existed still work: true is always, false is never.
func ParseCookieSecureMode(v string) (CookieSecureMode, error) {
	switch m := CookieSecureMode(strings.ToLower(strings.TrimSpace(v))); m {
	case CookieSecureAuto, CookieSecureAlways, CookieSecureNever:
		return m, nil
	case "true":
		return CookieSecureAlways, nil
	case "false":
		return CookieSecureNever, nil
	default:
		return "", fmt.Errorf("auth.session.cookieSecure %q is not one of auto, always, never", v)
	}
}

// UnmarshalYAML accepts a mode string or a boolean (the pre-mode form).
func (m *CookieSecureMode) UnmarshalYAML(n *yaml.Node) error {
	var v string
	if err := n.Decode(&v); err != nil {
		return fmt.Errorf("auth.session.cookieSecure: %w", err)
	}
	parsed, err := ParseCookieSecureMode(v)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// AuthConfig configures users, sessions and API keys.
type AuthConfig struct {
	Argon2 Argon2Config `yaml:"argon2"`
	// MinPasswordLength and MaxPasswordLength bound accepted passwords (the cap
	// keeps hashing cost predictable).
	MinPasswordLength int `yaml:"minPasswordLength"`
	MaxPasswordLength int `yaml:"maxPasswordLength"`

	Session    SessionConfig    `yaml:"session"`
	APIKey     APIKeyConfig     `yaml:"apiKey"`
	LoginLimit LoginLimitConfig `yaml:"loginLimit"`
}

// Argon2Config holds the argon2id cost parameters. They are stored with every
// hash, so changing them later only affects new hashes.
type Argon2Config struct {
	// Iterations is the time cost.
	Iterations uint32 `yaml:"iterations"`
	// MemoryKiB is the memory cost in kibibytes.
	MemoryKiB uint32 `yaml:"memoryKib"`
	// Threads is the parallelism.
	Threads uint8 `yaml:"threads"`
	// SaltBytes and KeyBytes size the salt and the derived key.
	SaltBytes uint32 `yaml:"saltBytes"`
	KeyBytes  uint32 `yaml:"keyBytes"`
	// MaxConcurrent caps how many hashes run at once. Each needs MemoryKiB of
	// RAM, so this bounds the memory spike of parallel logins; extra requests
	// wait. It is not part of the stored hash.
	MaxConcurrent int `yaml:"maxConcurrent"`
	// ReleaseMemory returns the hash's working memory to the OS as soon as no
	// hash is running (debug.FreeOSMemory). Without it the Go runtime keeps
	// roughly MemoryKiB of garbage in RSS until the next collection.
	ReleaseMemory bool `yaml:"releaseMemory"`
}

// SessionConfig configures browser sessions and their cookie.
type SessionConfig struct {
	// IdleTTL ends a session that was not used for this long.
	IdleTTL time.Duration `yaml:"idleTtl"`
	// AbsoluteTTL ends a session this long after login, however active.
	AbsoluteTTL time.Duration `yaml:"absoluteTtl"`
	// TokenBytes is the size of the random session token.
	TokenBytes int `yaml:"tokenBytes"`
	// TouchInterval limits how often a session's last-use time is written.
	TouchInterval time.Duration `yaml:"touchInterval"`
	// PurgeInterval is how often expired sessions are deleted.
	PurgeInterval time.Duration `yaml:"purgeInterval"`

	CookieName   string `yaml:"cookieName"`
	CookiePath   string `yaml:"cookiePath"`
	CookieDomain string `yaml:"cookieDomain"`
	// CookieSecure is the Secure attribute mode: auto, always or never.
	CookieSecure   CookieSecureMode `yaml:"cookieSecure"`
	CookieSameSite string           `yaml:"cookieSameSite"`
	// CSRFHeader carries the CSRF token on cookie-authenticated mutations.
	CSRFHeader string `yaml:"csrfHeader"`
}

// APIKeyConfig configures API keys.
type APIKeyConfig struct {
	// Prefix starts every key, so leaked keys are recognizable.
	Prefix string `yaml:"prefix"`
	// LookupBytes sizes the random, non-secret part used to find a key.
	LookupBytes int `yaml:"lookupBytes"`
	// SecretBytes sizes the secret part.
	SecretBytes int `yaml:"secretBytes"`
	// MaxPerUser caps the active keys of one user.
	MaxPerUser int `yaml:"maxPerUser"`
	// TouchInterval limits how often a key's last-use time is written.
	TouchInterval time.Duration `yaml:"touchInterval"`
}

// LoginLimitConfig throttles failed logins per account and per client address.
type LoginLimitConfig struct {
	Enabled bool `yaml:"enabled"`
	// MaxFailures within Window blocks further attempts until the oldest
	// failure leaves the window.
	MaxFailures int `yaml:"maxFailures"`
	// MaxFailuresPerAddress is the same limit counted over all accounts for
	// one client address, so one host cannot guess across many accounts.
	MaxFailuresPerAddress int           `yaml:"maxFailuresPerAddress"`
	Window                time.Duration `yaml:"window"`
	// MaxTrackedKeys bounds the memory of the in-process limiter.
	MaxTrackedKeys int `yaml:"maxTrackedKeys"`
}

func defaultAuth() AuthConfig {
	const kib = 1024
	return AuthConfig{
		// OWASP's first-choice argon2id setting: m=19 MiB, t=2, p=1. Each hash
		// allocates MemoryKiB, so a higher cost raises peak RSS directly.
		Argon2: Argon2Config{
			Iterations: 2, MemoryKiB: 19 * kib, Threads: 1, SaltBytes: 16, KeyBytes: 32, MaxConcurrent: 2, ReleaseMemory: true,
		},
		MinPasswordLength: 10,
		MaxPasswordLength: 256,
		Session: SessionConfig{
			IdleTTL:        24 * time.Hour,
			AbsoluteTTL:    7 * 24 * time.Hour,
			TokenBytes:     32,
			TouchInterval:  time.Minute,
			PurgeInterval:  10 * time.Minute,
			CookieName:     "engi_session",
			CookiePath:     "/",
			CookieSecure:   CookieSecureAlways,
			CookieSameSite: SameSiteLax,
			CSRFHeader:     DefaultCSRFHeader,
		},
		APIKey: APIKeyConfig{
			Prefix: "engi", LookupBytes: 6, SecretBytes: 32, MaxPerUser: 50, TouchInterval: time.Minute,
		},
		LoginLimit: LoginLimitConfig{Enabled: true, MaxFailures: 5, MaxFailuresPerAddress: 20, Window: 15 * time.Minute, MaxTrackedKeys: 10000},
	}
}

// Validate reports every problem with the auth settings.
func (c AuthConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "auth.argon2.iterations", int64(c.Argon2.Iterations))
	errs = appendPositive(errs, "auth.argon2.memoryKib", int64(c.Argon2.MemoryKiB))
	errs = appendPositive(errs, "auth.argon2.threads", int64(c.Argon2.Threads))
	errs = appendPositive(errs, "auth.argon2.saltBytes", int64(c.Argon2.SaltBytes))
	errs = appendPositive(errs, "auth.argon2.keyBytes", int64(c.Argon2.KeyBytes))
	errs = appendPositive(errs, "auth.argon2.maxConcurrent", int64(c.Argon2.MaxConcurrent))
	errs = appendPositive(errs, "auth.minPasswordLength", int64(c.MinPasswordLength))
	if c.MaxPasswordLength < c.MinPasswordLength {
		errs = append(errs, errors.New("auth.maxPasswordLength must not be smaller than auth.minPasswordLength"))
	}
	s := c.Session
	errs = appendPositive(errs, "auth.session.idleTtl", int64(s.IdleTTL))
	errs = appendPositive(errs, "auth.session.absoluteTtl", int64(s.AbsoluteTTL))
	errs = appendPositive(errs, "auth.session.tokenBytes", int64(s.TokenBytes))
	errs = appendPositive(errs, "auth.session.touchInterval", int64(s.TouchInterval))
	errs = appendPositive(errs, "auth.session.purgeInterval", int64(s.PurgeInterval))
	if s.IdleTTL > s.AbsoluteTTL && s.AbsoluteTTL > 0 {
		errs = append(errs, errors.New("auth.session.idleTtl must not exceed auth.session.absoluteTtl"))
	}
	if s.TokenBytes > 0 && s.TokenBytes < 16 {
		errs = append(errs, errors.New("auth.session.tokenBytes must be at least 16"))
	}
	if s.CookieName == "" || s.CSRFHeader == "" || s.CookiePath == "" {
		errs = append(errs, errors.New("auth.session.cookieName, cookiePath and csrfHeader must not be empty"))
	}
	if !slices.Contains([]string{SameSiteLax, SameSiteStrict, SameSiteNone}, s.CookieSameSite) {
		errs = append(errs, fmt.Errorf("auth.session.cookieSameSite %q is not one of lax, strict, none", s.CookieSameSite))
	}
	if !slices.Contains([]CookieSecureMode{CookieSecureAuto, CookieSecureAlways, CookieSecureNever}, s.CookieSecure) {
		errs = append(errs, fmt.Errorf("auth.session.cookieSecure %q is not one of auto, always, never", s.CookieSecure))
	}
	if s.CookieSameSite == SameSiteNone && s.CookieSecure == CookieSecureNever {
		errs = append(errs, errors.New("auth.session.cookieSameSite none requires auth.session.cookieSecure auto or always"))
	}
	k := c.APIKey
	if k.Prefix == "" {
		errs = append(errs, errors.New("auth.apiKey.prefix must not be empty"))
	}
	errs = appendPositive(errs, "auth.apiKey.lookupBytes", int64(k.LookupBytes))
	errs = appendPositive(errs, "auth.apiKey.maxPerUser", int64(k.MaxPerUser))
	errs = appendPositive(errs, "auth.apiKey.touchInterval", int64(k.TouchInterval))
	if k.SecretBytes < 16 {
		errs = append(errs, errors.New("auth.apiKey.secretBytes must be at least 16"))
	}
	if l := c.LoginLimit; l.Enabled {
		errs = appendPositive(errs, "auth.loginLimit.maxFailures", int64(l.MaxFailures))
		errs = appendPositive(errs, "auth.loginLimit.maxFailuresPerAddress", int64(l.MaxFailuresPerAddress))
		errs = appendPositive(errs, "auth.loginLimit.window", int64(l.Window))
		errs = appendPositive(errs, "auth.loginLimit.maxTrackedKeys", int64(l.MaxTrackedKeys))
	}
	return errors.Join(errs...)
}

func u32Setting(dst func(*Config) *uint32) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := parseUint(v, 32)
		*dst(c) = uint32(n) //nolint:gosec // bounded by ParseUint bitSize
		return err
	}
}

// authSettings are the env/flag bindings of the auth section.
func authSettings() []setting {
	return []setting{
		{"ENGI_AUTH_ARGON2_ITERATIONS", "auth-argon2-iterations", "argon2id iterations",
			u32Setting(func(c *Config) *uint32 { return &c.Auth.Argon2.Iterations })},
		{"ENGI_AUTH_ARGON2_MEMORY_KIB", "auth-argon2-memory-kib", "argon2id memory in KiB",
			u32Setting(func(c *Config) *uint32 { return &c.Auth.Argon2.MemoryKiB })},
		{"ENGI_AUTH_ARGON2_THREADS", "auth-argon2-threads", "argon2id parallelism",
			func(c *Config, v string) error {
				n, err := parseUint(v, 8)
				c.Auth.Argon2.Threads = uint8(n) //nolint:gosec // bounded by ParseUint bitSize
				return err
			}},
		{"ENGI_AUTH_ARGON2_MAX_CONCURRENT", "auth-argon2-max-concurrent", "password hashes allowed to run at once",
			intSetting(func(c *Config) *int { return &c.Auth.Argon2.MaxConcurrent })},
		{"ENGI_AUTH_ARGON2_RELEASE_MEMORY", "auth-argon2-release-memory", "return hashing memory to the OS after each hash",
			boolSetting(func(c *Config) *bool { return &c.Auth.Argon2.ReleaseMemory })},
		{"ENGI_AUTH_MIN_PASSWORD_LENGTH", "auth-min-password-length", "shortest accepted password",
			intSetting(func(c *Config) *int { return &c.Auth.MinPasswordLength })},
		{"ENGI_AUTH_SESSION_IDLE_TTL", "auth-session-idle-ttl", "session idle expiry",
			durationSetting(func(c *Config) *time.Duration { return &c.Auth.Session.IdleTTL })},
		{"ENGI_AUTH_SESSION_ABSOLUTE_TTL", "auth-session-absolute-ttl", "session absolute expiry",
			durationSetting(func(c *Config) *time.Duration { return &c.Auth.Session.AbsoluteTTL })},
		{"ENGI_AUTH_SESSION_COOKIE_NAME", "auth-session-cookie-name", "session cookie name",
			stringSetting(func(c *Config) *string { return &c.Auth.Session.CookieName })},
		{"ENGI_AUTH_SESSION_COOKIE_DOMAIN", "auth-session-cookie-domain", "session cookie Domain attribute",
			stringSetting(func(c *Config) *string { return &c.Auth.Session.CookieDomain })},
		{"ENGI_AUTH_SESSION_COOKIE_SECURE", "auth-session-cookie-secure", "Secure cookie attribute: auto, always, never",
			func(c *Config, v string) (err error) {
				c.Auth.Session.CookieSecure, err = ParseCookieSecureMode(v)
				return
			}},
		{"ENGI_AUTH_SESSION_COOKIE_SAME_SITE", "auth-session-cookie-same-site", "cookie SameSite: lax, strict, none",
			stringSetting(func(c *Config) *string { return &c.Auth.Session.CookieSameSite })},
		{"ENGI_AUTH_API_KEY_PREFIX", "auth-api-key-prefix", "prefix of new API keys",
			stringSetting(func(c *Config) *string { return &c.Auth.APIKey.Prefix })},
		{"ENGI_AUTH_API_KEY_MAX_PER_USER", "auth-api-key-max-per-user", "active API keys per user",
			intSetting(func(c *Config) *int { return &c.Auth.APIKey.MaxPerUser })},
		{"ENGI_AUTH_LOGIN_LIMIT_ENABLED", "auth-login-limit-enabled", "throttle failed logins",
			boolSetting(func(c *Config) *bool { return &c.Auth.LoginLimit.Enabled })},
		{"ENGI_AUTH_LOGIN_LIMIT_MAX_FAILURES", "auth-login-limit-max-failures", "failed logins allowed per window",
			intSetting(func(c *Config) *int { return &c.Auth.LoginLimit.MaxFailures })},
		{"ENGI_AUTH_LOGIN_LIMIT_WINDOW", "auth-login-limit-window", "failed login counting window",
			durationSetting(func(c *Config) *time.Duration { return &c.Auth.LoginLimit.Window })},
	}
}
