package config

import (
	"errors"
	"time"
)

// PollConfig tunes the host of polling triggers (IMAP, ...). Pollers run on
// the leader only.
type PollConfig struct {
	// MinInterval is the shortest poll interval a trigger node may ask for.
	MinInterval time.Duration `yaml:"minInterval"`
	// MaxFailures is how many polls in a row may fail before the workflow is
	// deactivated with the last error recorded.
	MaxFailures int `yaml:"maxFailures"`
	// MaxBackoff caps the wait after a failed poll (it doubles from the
	// trigger's interval).
	MaxBackoff time.Duration `yaml:"maxBackoff"`
}

func defaultPoll() PollConfig {
	return PollConfig{MinInterval: 15 * time.Second, MaxFailures: 10, MaxBackoff: 10 * time.Minute}
}

func (p PollConfig) validate() []error {
	var errs []error
	errs = appendPositive(errs, "poll.minInterval", int64(p.MinInterval))
	errs = appendPositive(errs, "poll.maxFailures", int64(p.MaxFailures))
	errs = appendPositive(errs, "poll.maxBackoff", int64(p.MaxBackoff))
	if p.MaxBackoff < p.MinInterval {
		errs = append(errs, errors.New("poll.maxBackoff must not be shorter than poll.minInterval"))
	}
	return errs
}

func pollSettings() []setting {
	return []setting{
		{"ENGI_POLL_MIN_INTERVAL", "poll-min-interval", "shortest interval a polling trigger may use",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Poll.MinInterval })},
		{"ENGI_POLL_MAX_FAILURES", "poll-max-failures", "failed polls in a row before the workflow is deactivated",
			intSetting(func(c *Config) *int { return &c.Trigger.Poll.MaxFailures })},
		{"ENGI_POLL_MAX_BACKOFF", "poll-max-backoff", "longest wait after a failed poll",
			durationSetting(func(c *Config) *time.Duration { return &c.Trigger.Poll.MaxBackoff })},
	}
}
