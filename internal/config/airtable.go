package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// airtableMaxBatch is the most records Airtable accepts in one write request.
const airtableMaxBatch = 10

// AirtableNodeConfig tunes the Airtable node. The base URL exists so the node
// can target a test server.
type AirtableNodeConfig struct {
	// BaseURL is the Airtable REST API root (the version segment included).
	BaseURL string `yaml:"baseUrl"`
	// MaxRetryWait caps how long one rate-limit (HTTP 429) wait may last. A
	// longer Retry-After fails the call instead of blocking the worker.
	MaxRetryWait time.Duration `yaml:"maxRetryWait"`
	// MaxRetries is how many times a rate-limited call is retried.
	MaxRetries int `yaml:"maxRetries"`
	// MaxListResults caps the records one "list records" run returns per input item.
	MaxListResults int `yaml:"maxListResults"`
	// PageSize is the page size requested from the list endpoint.
	PageSize int `yaml:"pageSize"`
	// BatchSize is the records per write request. Airtable accepts at most 10.
	BatchSize int `yaml:"batchSize"`
}

func defaultAirtable() AirtableNodeConfig {
	return AirtableNodeConfig{
		BaseURL:      "https://api.airtable.com/v0",
		MaxRetryWait: 30 * time.Second, MaxRetries: 2, MaxListResults: 1000, PageSize: 100, BatchSize: airtableMaxBatch,
	}
}

// Validate reports every problem with the Airtable node settings.
func (c AirtableNodeConfig) Validate() error {
	var errs []error
	if u, err := url.Parse(c.BaseURL); err != nil || !u.IsAbs() || u.Host == "" {
		errs = append(errs, fmt.Errorf("nodes.airtable.baseUrl %q is not an absolute URL", c.BaseURL))
	}
	errs = appendPositive(errs, "nodes.airtable.maxRetryWait", int64(c.MaxRetryWait))
	errs = appendPositive(errs, "nodes.airtable.maxListResults", int64(c.MaxListResults))
	errs = appendPositive(errs, "nodes.airtable.pageSize", int64(c.PageSize))
	if c.BatchSize < 1 || c.BatchSize > airtableMaxBatch {
		errs = append(errs, fmt.Errorf("nodes.airtable.batchSize must be between 1 and %d (Airtable's limit)", airtableMaxBatch))
	}
	if c.MaxRetries < 0 {
		errs = append(errs, errors.New("nodes.airtable.maxRetries must not be negative"))
	}
	return errors.Join(errs...)
}

func airtableSettings() []setting {
	return []setting{
		{"ENGI_NODES_AIRTABLE_BASE_URL", "nodes-airtable-base-url", "Airtable API base URL",
			stringSetting(func(c *Config) *string { return &c.Nodes.Airtable.BaseURL })},
		{"ENGI_NODES_AIRTABLE_MAX_RETRY_WAIT", "nodes-airtable-max-retry-wait", "longest rate-limit wait the Airtable node accepts",
			durationSetting(func(c *Config) *time.Duration { return &c.Nodes.Airtable.MaxRetryWait })},
		{"ENGI_NODES_AIRTABLE_MAX_RETRIES", "nodes-airtable-max-retries", "retries of a rate-limited Airtable call",
			intSetting(func(c *Config) *int { return &c.Nodes.Airtable.MaxRetries })},
		{"ENGI_NODES_AIRTABLE_MAX_LIST_RESULTS", "nodes-airtable-max-list-results", "max records one Airtable list returns per item",
			intSetting(func(c *Config) *int { return &c.Nodes.Airtable.MaxListResults })},
		{"ENGI_NODES_AIRTABLE_PAGE_SIZE", "nodes-airtable-page-size", "page size of Airtable list calls",
			intSetting(func(c *Config) *int { return &c.Nodes.Airtable.PageSize })},
		{"ENGI_NODES_AIRTABLE_BATCH_SIZE", "nodes-airtable-batch-size", "records per Airtable write request (max 10)",
			intSetting(func(c *Config) *int { return &c.Nodes.Airtable.BatchSize })},
	}
}
