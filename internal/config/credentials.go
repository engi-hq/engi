package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Secret is a string that never prints itself. Use Reveal to read it.
type Secret string

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer.
func (s Secret) GoString() string { return redacted }

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

const redacted = "[redacted]"

// CredentialsConfig configures credential encryption and handling.
type CredentialsConfig struct {
	// ActiveKeyID names the key used to encrypt new and updated credentials.
	ActiveKeyID string `yaml:"activeKeyId"`
	// Keys maps key IDs to base64-encoded 32-byte AES keys. Prefer supplying
	// them through ENGI_ENCRYPTION_KEYS or KeysFile instead of a config file.
	// There is no default: in server mode startup fails without keys.
	Keys map[string]Secret `yaml:"keys"`
	// KeysFile is a file with one "keyId:base64key" per line; its keys are
	// merged into Keys.
	KeysFile string `yaml:"keysFile"`
	// RotationBatchSize is how many credentials one re-encryption batch handles.
	RotationBatchSize int `yaml:"rotationBatchSize"`
	// OAuth2RefreshSkew refreshes an access token this long before it expires.
	OAuth2RefreshSkew time.Duration `yaml:"oauth2RefreshSkew"`
	// OAuth2StateTTL is how long an authorization-code state stays valid.
	OAuth2StateTTL time.Duration `yaml:"oauth2StateTtl"`
	// OAuth2RedirectURL is the exact callback URL registered with providers.
	OAuth2RedirectURL string `yaml:"oauth2RedirectUrl"`
	// OAuth2RequestTimeout bounds one token endpoint call.
	OAuth2RequestTimeout time.Duration `yaml:"oauth2RequestTimeout"`
	// OAuth2RefreshLockTimeout bounds how long a refresh waits for the
	// cluster-wide lock of its credential, held by another replica while it
	// refreshes the same token. A wait past it fails the call.
	OAuth2RefreshLockTimeout time.Duration `yaml:"oauth2RefreshLockTimeout"`
	// TestTimeout bounds one credential test.
	TestTimeout time.Duration `yaml:"testTimeout"`
}

func defaultCredentials() CredentialsConfig {
	return CredentialsConfig{
		RotationBatchSize:    100,
		OAuth2RefreshSkew:    time.Minute,
		OAuth2StateTTL:       10 * time.Minute,
		OAuth2RequestTimeout: 15 * time.Second,
		// Twice the request timeout: a waiter outlasts one slow token call of the holder.
		OAuth2RefreshLockTimeout: 30 * time.Second,
		TestTimeout:              15 * time.Second,
	}
}

// Enabled reports whether a keyring is configured.
func (c CredentialsConfig) Enabled() bool { return len(c.Keys) > 0 || c.ActiveKeyID != "" }

// Validate checks the settings. A missing keyring is allowed here (standalone
// runs without credentials); use RequireKeyring in server mode.
func (c CredentialsConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "credentials.rotationBatchSize", int64(c.RotationBatchSize))
	errs = appendPositive(errs, "credentials.oauth2RefreshSkew", int64(c.OAuth2RefreshSkew))
	errs = appendPositive(errs, "credentials.oauth2StateTtl", int64(c.OAuth2StateTTL))
	errs = appendPositive(errs, "credentials.oauth2RequestTimeout", int64(c.OAuth2RequestTimeout))
	errs = appendPositive(errs, "credentials.oauth2RefreshLockTimeout", int64(c.OAuth2RefreshLockTimeout))
	errs = appendPositive(errs, "credentials.testTimeout", int64(c.TestTimeout))
	if c.Enabled() {
		errs = append(errs, c.validateKeyring())
	}
	return errors.Join(errs...)
}

func (c CredentialsConfig) validateKeyring() error {
	var errs []error
	if c.ActiveKeyID == "" {
		errs = append(errs, errors.New("credentials.activeKeyId must be set when keys are configured (ENGI_ENCRYPTION_ACTIVE_KEY_ID)"))
	} else if _, ok := c.Keys[c.ActiveKeyID]; !ok {
		errs = append(errs, fmt.Errorf("credentials.activeKeyId %q has no matching key", c.ActiveKeyID))
	}
	for id, k := range c.Keys {
		if id == "" || strings.ContainsAny(id, ": \t\n") {
			errs = append(errs, fmt.Errorf("credentials key id %q must be non-empty without colons or spaces", id))
		}
		if k == "" {
			errs = append(errs, fmt.Errorf("credentials key %q is empty", id))
		}
	}
	return errors.Join(errs...)
}

// RequireKeyring fails with a clear message when no usable keyring is
// configured. Server roles call it at startup.
func (c CredentialsConfig) RequireKeyring() error {
	if len(c.Keys) == 0 {
		return errors.New("no credential encryption key configured: set ENGI_ENCRYPTION_KEYS (keyId:base64key) " +
			"or ENGI_ENCRYPTION_KEYS_FILE, and ENGI_ENCRYPTION_ACTIVE_KEY_ID; generate a key with `openssl rand -base64 32`")
	}
	return c.validateKeyring()
}

// parseKeyList parses "id:base64,id2:base64" or newline separated entries.
func parseKeyList(v string) (map[string]Secret, error) {
	out := map[string]Secret{}
	for _, entry := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		id, key, ok := strings.Cut(entry, ":")
		if !ok || id == "" || key == "" {
			return nil, errors.New("key entries must look like keyId:base64key")
		}
		out[strings.TrimSpace(id)] = Secret(strings.TrimSpace(key))
	}
	return out, nil
}

func credentialSettings() []setting {
	return []setting{
		{"ENGI_ENCRYPTION_ACTIVE_KEY_ID", "encryption-active-key-id", "key id used to encrypt new credentials",
			func(c *Config, v string) error { c.Credentials.ActiveKeyID = v; return nil }},
		{"ENGI_ENCRYPTION_KEYS", "", "comma-separated keyId:base64key entries (no flag: keep keys out of argv)",
			func(c *Config, v string) error {
				keys, err := parseKeyList(v)
				if err != nil {
					return err
				}
				c.Credentials.mergeKeys(keys)
				return nil
			}},
		{"ENGI_ENCRYPTION_KEYS_FILE", "encryption-keys-file", "file with one keyId:base64key per line",
			func(c *Config, v string) error { c.Credentials.KeysFile = v; return nil }},
		{"ENGI_CREDENTIALS_ROTATION_BATCH_SIZE", "credentials-rotation-batch-size", "credentials re-encrypted per batch",
			func(c *Config, v string) (err error) { c.Credentials.RotationBatchSize, err = strconv.Atoi(v); return }},
		{"ENGI_CREDENTIALS_OAUTH2_REFRESH_SKEW", "credentials-oauth2-refresh-skew", "refresh tokens this long before expiry",
			func(c *Config, v string) (err error) {
				c.Credentials.OAuth2RefreshSkew, err = time.ParseDuration(v)
				return
			}},
		{"ENGI_CREDENTIALS_OAUTH2_STATE_TTL", "credentials-oauth2-state-ttl", "lifetime of an authorization state",
			func(c *Config, v string) (err error) {
				c.Credentials.OAuth2StateTTL, err = time.ParseDuration(v)
				return
			}},
		{"ENGI_CREDENTIALS_OAUTH2_REDIRECT_URL", "credentials-oauth2-redirect-url", "OAuth2 callback URL",
			func(c *Config, v string) error { c.Credentials.OAuth2RedirectURL = v; return nil }},
		{"ENGI_CREDENTIALS_OAUTH2_REQUEST_TIMEOUT", "credentials-oauth2-request-timeout", "timeout of one token request",
			func(c *Config, v string) (err error) {
				c.Credentials.OAuth2RequestTimeout, err = time.ParseDuration(v)
				return
			}},
		{"ENGI_CREDENTIALS_OAUTH2_REFRESH_LOCK_TIMEOUT", "credentials-oauth2-refresh-lock-timeout", "longest wait for another replica's refresh of the same credential",
			func(c *Config, v string) (err error) {
				c.Credentials.OAuth2RefreshLockTimeout, err = time.ParseDuration(v)
				return
			}},
		{"ENGI_CREDENTIALS_TEST_TIMEOUT", "credentials-test-timeout", "timeout of a credential test",
			func(c *Config, v string) (err error) { c.Credentials.TestTimeout, err = time.ParseDuration(v); return }},
	}
}

func (c *CredentialsConfig) mergeKeys(keys map[string]Secret) {
	if c.Keys == nil {
		c.Keys = map[string]Secret{}
	}
	for id, k := range keys {
		c.Keys[id] = k
	}
}

// loadKeysFile merges the keys of KeysFile into Keys.
func (c *CredentialsConfig) loadKeysFile(read func(string) ([]byte, error)) error {
	if c.KeysFile == "" {
		return nil
	}
	raw, err := read(c.KeysFile)
	if err != nil {
		return fmt.Errorf("reading credentials keys file %q: %w", c.KeysFile, err)
	}
	keys, err := parseKeyList(string(raw))
	if err != nil {
		return fmt.Errorf("credentials keys file %q: %w", c.KeysFile, err)
	}
	c.mergeKeys(keys)
	return nil
}
