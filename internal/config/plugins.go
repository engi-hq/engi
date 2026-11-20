package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PluginsConfig configures any-language plugins (docs/15).
type PluginsConfig struct {
	// Enabled loads the active version of every installed plugin at start.
	Enabled bool `yaml:"enabled"`
	// Dir holds installed plugins: <dir>/<name>/<version>/ and <dir>/<name>/active.
	Dir string `yaml:"dir"`
	// InheritEnv names the server's environment variables a stdio plugin
	// process receives (on top of its manifest's env). Nothing else is passed.
	InheritEnv []string `yaml:"inheritEnv"`
	// StartTimeout bounds starting a plugin and its initialize call.
	StartTimeout time.Duration `yaml:"startTimeout"`
	// CallTimeout bounds one execute call.
	CallTimeout time.Duration `yaml:"callTimeout"`
	// ShutdownGrace is how long a plugin gets to exit before it is killed.
	ShutdownGrace time.Duration `yaml:"shutdownGrace"`
	// IdleTimeout stops a stdio plugin after this long without calls; it is
	// started again on demand. Zero keeps it running.
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// RestartBackoff is the least time between two starts of a plugin.
	RestartBackoff time.Duration `yaml:"restartBackoff"`
	// MaxConcurrentCalls caps calls in flight to one plugin.
	MaxConcurrentCalls int `yaml:"maxConcurrentCalls"`
	// MaxMessageBytes caps one protocol message in either direction.
	MaxMessageBytes int `yaml:"maxMessageBytes"`
	// MaxBlobBytes caps the bytes of one blob.read or blob.write callback.
	MaxBlobBytes int `yaml:"maxBlobBytes"`
	// MaxLogLineBytes cuts long stderr lines of a plugin.
	MaxLogLineBytes int `yaml:"maxLogLineBytes"`
	// StartErrorLines is how many of a plugin's last stderr lines a failed
	// start reports, each cut at MaxLogLineBytes. Zero reports none.
	StartErrorLines int `yaml:"startErrorLines"`
}

func defaultPlugins() PluginsConfig {
	return PluginsConfig{
		Enabled: true, Dir: "plugins",
		InheritEnv:   []string{"PATH", "HOME", "LANG", "TZ", "TMPDIR", "SYSTEMROOT"},
		StartTimeout: 10 * time.Second, CallTimeout: 5 * time.Minute, ShutdownGrace: 5 * time.Second,
		RestartBackoff: time.Second, MaxConcurrentCalls: 16,
		MaxMessageBytes: 32 << 20, MaxBlobBytes: 16 << 20, MaxLogLineBytes: 4 << 10,
		StartErrorLines: 20,
	}
}

// base64Overhead is the size of base64 text per input byte, rounded up, so a
// blob plus its JSON envelope fits in one message.
const base64Overhead = 4.0 / 3

// Validate reports every problem with the plugin settings.
func (c PluginsConfig) Validate() error {
	var errs []error
	if c.Enabled && c.Dir == "" {
		errs = append(errs, errors.New("plugins.dir must not be empty when plugins are enabled"))
	}
	errs = appendPositive(errs, "plugins.startTimeout", int64(c.StartTimeout))
	errs = appendPositive(errs, "plugins.callTimeout", int64(c.CallTimeout))
	errs = appendPositive(errs, "plugins.shutdownGrace", int64(c.ShutdownGrace))
	errs = appendPositive(errs, "plugins.restartBackoff", int64(c.RestartBackoff))
	errs = appendPositive(errs, "plugins.maxConcurrentCalls", int64(c.MaxConcurrentCalls))
	errs = appendPositive(errs, "plugins.maxMessageBytes", int64(c.MaxMessageBytes))
	errs = appendPositive(errs, "plugins.maxBlobBytes", int64(c.MaxBlobBytes))
	errs = appendPositive(errs, "plugins.maxLogLineBytes", int64(c.MaxLogLineBytes))
	if c.StartErrorLines < 0 {
		errs = append(errs, errors.New("plugins.startErrorLines must not be negative"))
	}
	if c.IdleTimeout < 0 {
		errs = append(errs, errors.New("plugins.idleTimeout must not be negative"))
	}
	for _, name := range c.InheritEnv {
		if strings.HasPrefix(strings.ToUpper(name), envPrefix) {
			errs = append(errs, fmt.Errorf("plugins.inheritEnv: %s is an Engi setting and is never passed to plugins", name))
		}
	}
	if float64(c.MaxBlobBytes)*base64Overhead >= float64(c.MaxMessageBytes) {
		errs = append(errs, fmt.Errorf("plugins.maxBlobBytes (%d) must fit in plugins.maxMessageBytes (%d) once base64-encoded", c.MaxBlobBytes, c.MaxMessageBytes))
	}
	return errors.Join(errs...)
}

func pluginSettings() []setting {
	return []setting{
		{"ENGI_PLUGINS_ENABLED", "plugins-enabled", "load installed plugins at start", boolSetting(func(c *Config) *bool { return &c.Plugins.Enabled })},
		{"ENGI_PLUGINS_DIR", "plugins-dir", "directory of installed plugins", stringSetting(func(c *Config) *string { return &c.Plugins.Dir })},
		{"ENGI_PLUGINS_INHERIT_ENV", "plugins-inherit-env", "comma-separated env vars passed to plugin processes", listSetting(func(c *Config) *[]string { return &c.Plugins.InheritEnv })},
		{"ENGI_PLUGINS_START_TIMEOUT", "plugins-start-timeout", "max time to start a plugin", durationSetting(func(c *Config) *time.Duration { return &c.Plugins.StartTimeout })},
		{"ENGI_PLUGINS_CALL_TIMEOUT", "plugins-call-timeout", "max time of one plugin call", durationSetting(func(c *Config) *time.Duration { return &c.Plugins.CallTimeout })},
		{"ENGI_PLUGINS_SHUTDOWN_GRACE", "plugins-shutdown-grace", "time a plugin gets to exit before it is killed", durationSetting(func(c *Config) *time.Duration { return &c.Plugins.ShutdownGrace })},
		{"ENGI_PLUGINS_IDLE_TIMEOUT", "plugins-idle-timeout", "stop idle plugin processes after this long (0 never)", durationSetting(func(c *Config) *time.Duration { return &c.Plugins.IdleTimeout })},
		{"ENGI_PLUGINS_RESTART_BACKOFF", "plugins-restart-backoff", "least time between two starts of a plugin", durationSetting(func(c *Config) *time.Duration { return &c.Plugins.RestartBackoff })},
		{"ENGI_PLUGINS_MAX_CONCURRENT_CALLS", "plugins-max-concurrent-calls", "calls in flight per plugin", intSetting(func(c *Config) *int { return &c.Plugins.MaxConcurrentCalls })},
		{"ENGI_PLUGINS_MAX_MESSAGE_BYTES", "plugins-max-message-bytes", "max bytes of one plugin protocol message", intSetting(func(c *Config) *int { return &c.Plugins.MaxMessageBytes })},
		{"ENGI_PLUGINS_MAX_BLOB_BYTES", "plugins-max-blob-bytes", "max bytes of one plugin blob callback", intSetting(func(c *Config) *int { return &c.Plugins.MaxBlobBytes })},
		{"ENGI_PLUGINS_MAX_LOG_LINE_BYTES", "plugins-max-log-line-bytes", "cut plugin stderr lines at this length", intSetting(func(c *Config) *int { return &c.Plugins.MaxLogLineBytes })},
		{"ENGI_PLUGINS_START_ERROR_LINES", "plugins-start-error-lines", "last stderr lines of a plugin that fails to start (0 none)", intSetting(func(c *Config) *int { return &c.Plugins.StartErrorLines })},
	}
}
