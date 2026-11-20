package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// GoogleNodeConfig tunes the Google Sheets node. The base URL exists so the
// node can target a local emulator or a test server.
type GoogleNodeConfig struct {
	// SheetsBaseURL is the Google Sheets API v4 root.
	SheetsBaseURL string `yaml:"sheetsBaseUrl"`
	// MaxRetryWait caps how long one rate-limit (HTTP 429) wait may last. A
	// longer Retry-After fails the call instead of blocking the worker.
	MaxRetryWait time.Duration `yaml:"maxRetryWait"`
	// MaxRetries is how many times a rate-limited call is retried.
	MaxRetries int `yaml:"maxRetries"`
	// MaxReadRows caps the rows one "read rows" run returns per input item.
	MaxReadRows int `yaml:"maxReadRows"`
}

func defaultGoogle() GoogleNodeConfig {
	return GoogleNodeConfig{
		SheetsBaseURL: "https://sheets.googleapis.com/v4",
		MaxRetryWait:  30 * time.Second, MaxRetries: 2, MaxReadRows: 10000,
	}
}

// Validate reports every problem with the Google node settings.
func (c GoogleNodeConfig) Validate() error {
	var errs []error
	if u, err := url.Parse(c.SheetsBaseURL); err != nil || !u.IsAbs() || u.Host == "" {
		errs = append(errs, fmt.Errorf("nodes.google.sheetsBaseUrl %q is not an absolute URL", c.SheetsBaseURL))
	}
	errs = appendPositive(errs, "nodes.google.maxRetryWait", int64(c.MaxRetryWait))
	errs = appendPositive(errs, "nodes.google.maxReadRows", int64(c.MaxReadRows))
	if c.MaxRetries < 0 {
		errs = append(errs, errors.New("nodes.google.maxRetries must not be negative"))
	}
	return errors.Join(errs...)
}

func googleSettings() []setting {
	return []setting{
		{"ENGI_NODES_GOOGLE_SHEETS_BASE_URL", "nodes-google-sheets-base-url", "Google Sheets API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.Google.SheetsBaseURL })},
		{"ENGI_NODES_GOOGLE_MAX_RETRY_WAIT", "nodes-google-max-retry-wait", "longest rate-limit wait the Google Sheets node accepts",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Google.MaxRetryWait })},
		{"ENGI_NODES_GOOGLE_MAX_RETRIES", "nodes-google-max-retries", "retries of a rate-limited Google Sheets call",
			intSetting(func(c *Config) *int { return &c.Nodes.Google.MaxRetries })},
		{"ENGI_NODES_GOOGLE_MAX_READ_ROWS", "nodes-google-max-read-rows", "max rows one Google Sheets read returns per item",
			intSetting(func(c *Config) *int { return &c.Nodes.Google.MaxReadRows })},
	}
}
