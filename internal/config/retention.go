package config

import (
	"errors"
	"time"
)

// RetentionConfig tunes the background job that deletes old finished
// executions (and their binary data). It runs on the leader only.
type RetentionConfig struct {
	// Enabled turns the job on. Off by default: pruning deletes data.
	Enabled bool `yaml:"enabled"`
	// Interval is the pause between two passes.
	Interval time.Duration `yaml:"interval"`
	// BatchSize is how many executions one delete round handles.
	BatchSize int `yaml:"batchSize"`
	// MaxAge is how long finished executions are kept, per terminal status,
	// counted from their start time. Zero keeps that status forever.
	MaxAge RetentionMaxAge `yaml:"maxAge"`
}

// RetentionMaxAge is the retention period of each terminal status.
type RetentionMaxAge struct {
	Success  time.Duration `yaml:"success"`
	Error    time.Duration `yaml:"error"`
	Canceled time.Duration `yaml:"canceled"`
	Crashed  time.Duration `yaml:"crashed"`
}

func defaultRetention() RetentionConfig {
	return RetentionConfig{
		Interval: time.Hour, BatchSize: 500,
		MaxAge: RetentionMaxAge{Success: 14 * 24 * time.Hour, Error: 30 * 24 * time.Hour, Canceled: 14 * 24 * time.Hour, Crashed: 30 * 24 * time.Hour},
	}
}

// Validate reports every problem with the retention settings.
func (c RetentionConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "retention.interval", int64(c.Interval))
	errs = appendPositive(errs, "retention.batchSize", int64(c.BatchSize))
	for name, d := range map[string]time.Duration{"success": c.MaxAge.Success, "error": c.MaxAge.Error, "canceled": c.MaxAge.Canceled, "crashed": c.MaxAge.Crashed} {
		if d < 0 {
			errs = append(errs, errors.New("retention.maxAge."+name+" must not be negative"))
		}
	}
	return errors.Join(errs...)
}

func retentionSettings() []setting {
	return []setting{
		{"ENGI_RETENTION_ENABLED", "retention-enabled", "prune old finished executions", boolSetting(func(c *Config) *bool { return &c.Retention.Enabled })},
		{"ENGI_RETENTION_INTERVAL", "retention-interval", "pause between pruning passes", durationSetting(func(c *Config) *time.Duration { return &c.Retention.Interval })},
		{"ENGI_RETENTION_BATCH_SIZE", "retention-batch-size", "executions deleted per round", intSetting(func(c *Config) *int { return &c.Retention.BatchSize })},
		{"ENGI_RETENTION_MAX_AGE_SUCCESS", "retention-max-age-success", "keep successful executions this long (0 forever)", durationSetting(func(c *Config) *time.Duration { return &c.Retention.MaxAge.Success })},
		{"ENGI_RETENTION_MAX_AGE_ERROR", "retention-max-age-error", "keep failed executions this long (0 forever)", durationSetting(func(c *Config) *time.Duration { return &c.Retention.MaxAge.Error })},
		{"ENGI_RETENTION_MAX_AGE_CANCELED", "retention-max-age-canceled", "keep canceled executions this long (0 forever)", durationSetting(func(c *Config) *time.Duration { return &c.Retention.MaxAge.Canceled })},
		{"ENGI_RETENTION_MAX_AGE_CRASHED", "retention-max-age-crashed", "keep crashed executions this long (0 forever)", durationSetting(func(c *Config) *time.Duration { return &c.Retention.MaxAge.Crashed })},
	}
}
