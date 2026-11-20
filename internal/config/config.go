// Package config holds Engi's typed configuration: its defaults, its loading
// from file, environment and flags, and its validation.
//
// Load order (later wins): defaults, optional YAML file, ENGI_* environment
// variables, command-line flags. Defaults are declared once, in Default.
package config

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Config is the complete runtime configuration.
type Config struct {
	Engine     EngineConfig     `yaml:"engine"`
	Expression ExpressionConfig `yaml:"expression"`
	Log        LogConfig        `yaml:"log"`
	Store      StoreConfig      `yaml:"store"`
	Queue      QueueConfig      `yaml:"queue"`
	Bus        BusConfig        `yaml:"bus"`
	Blob       BlobConfig       `yaml:"blob"`
	HTTPClient HTTPClientConfig `yaml:"httpClient"`
	Nodes      NodesConfig      `yaml:"nodes"`

	Credentials   CredentialsConfig   `yaml:"credentials"`
	Worker        WorkerConfig        `yaml:"worker"`
	Server        ServerConfig        `yaml:"server"`
	Auth          AuthConfig          `yaml:"auth"`
	Runtime       RuntimeConfig       `yaml:"runtime"`
	Observability ObservabilityConfig `yaml:"observability"`
	Retention     RetentionConfig     `yaml:"retention"`
	Workflow      WorkflowConfig      `yaml:"workflow"`
	Plugins       PluginsConfig       `yaml:"plugins"`

	// Trigger holds the webhook, schedule and lease sections (top-level keys).
	Trigger TriggerConfig `yaml:",inline"`
}

// EngineConfig tunes workflow execution.
type EngineConfig struct {
	// MaxNodeRuns caps how many times a single node may run in one execution
	// (loop guard).
	MaxNodeRuns int `yaml:"maxNodeRuns"`
	// DefaultNodeTimeout applies to nodes without their own timeout setting.
	DefaultNodeTimeout time.Duration `yaml:"defaultNodeTimeout"`
	// ExecutionTimeout is the wall-clock budget of one execution. A workflow
	// may lower or raise it through its own settings.
	ExecutionTimeout time.Duration `yaml:"executionTimeout"`
	// MaxSubworkflowDepth limits nested sub-workflow executions.
	MaxSubworkflowDepth int `yaml:"maxSubworkflowDepth"`
	// MaxRetryDelay caps the delay between two retry attempts.
	MaxRetryDelay time.Duration `yaml:"maxRetryDelay"`
}

// ExpressionConfig tunes the expression sandbox.
type ExpressionConfig struct {
	// EvalTimeout bounds the run time of one expression evaluation.
	EvalTimeout time.Duration `yaml:"evalTimeout"`
	// MaxTemplateLength bounds the size, in bytes, of an expression template.
	MaxTemplateLength int `yaml:"maxTemplateLength"`
	// MaxOutputBytes bounds the serialized size of an evaluation result.
	MaxOutputBytes int `yaml:"maxOutputBytes"`
	// MaxCallStackDepth bounds JavaScript recursion depth.
	MaxCallStackDepth int `yaml:"maxCallStackDepth"`
	// ProgramCacheSize is the number of compiled templates kept in the LRU cache.
	ProgramCacheSize int `yaml:"programCacheSize"`
	// EnvAllowList names the environment variables visible as $env.NAME.
	// Empty by default: expressions cannot read the environment.
	EnvAllowList []string `yaml:"envAllowList"`
}

// LogConfig controls logging.
type LogConfig struct {
	// Level is one of debug, info, warn, error.
	Level string `yaml:"level"`
	// Format is one of json, text.
	Format string `yaml:"format"`
}

// Supported log levels and formats.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"

	LogFormatJSON = "json"
	LogFormatText = "text"
)

// Default returns the configuration used when nothing else is specified.
// This is the single place where default values are declared.
func Default() Config {
	return Config{
		Engine: EngineConfig{
			MaxNodeRuns:         1000,
			DefaultNodeTimeout:  5 * time.Minute,
			ExecutionTimeout:    time.Hour,
			MaxSubworkflowDepth: 10,
			MaxRetryDelay:       5 * time.Minute,
		},
		Expression: ExpressionConfig{
			EvalTimeout:       2 * time.Second,
			MaxTemplateLength: 64 << 10,
			MaxOutputBytes:    1 << 20,
			MaxCallStackDepth: 128,
			ProgramCacheSize:  512,
			EnvAllowList:      nil,
		},
		Log: LogConfig{
			Level:  LogLevelInfo,
			Format: LogFormatJSON,
		},
		Store:      defaultStore(),
		Queue:      defaultQueue(),
		Bus:        defaultBus(),
		Blob:       defaultBlob(),
		HTTPClient: defaultHTTPClient(),
		Nodes:      defaultNodes(),

		Credentials:   defaultCredentials(),
		Worker:        defaultWorker(),
		Server:        defaultServer(),
		Auth:          defaultAuth(),
		Runtime:       defaultRuntime(),
		Observability: defaultObservability(),
		Retention:     defaultRetention(),
		Workflow:      defaultWorkflow(),
		Plugins:       defaultPlugins(),
		Trigger:       defaultTrigger(),
	}
}

// Validate reports every problem with the configuration, joined into one error.
func (c Config) Validate() error {
	return errors.Join(c.Engine.Validate(), c.Expression.Validate(), c.Log.Validate(),
		c.Store.Validate(), c.Queue.Validate(), c.Bus.Validate(), c.Blob.Validate(), c.HTTPClient.Validate(), c.Nodes.Validate(), c.Credentials.Validate(), c.Worker.Validate(),
		c.Server.Validate(), c.Auth.Validate(), c.Runtime.Validate(), c.Observability.Validate(), c.Retention.Validate(), c.Workflow.Validate(), c.Plugins.Validate(), c.Trigger.Validate())
}

func (c EngineConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "engine.maxNodeRuns", int64(c.MaxNodeRuns))
	errs = appendPositive(errs, "engine.defaultNodeTimeout", int64(c.DefaultNodeTimeout))
	errs = appendPositive(errs, "engine.executionTimeout", int64(c.ExecutionTimeout))
	errs = appendPositive(errs, "engine.maxRetryDelay", int64(c.MaxRetryDelay))
	if c.MaxSubworkflowDepth < 0 {
		errs = append(errs, errors.New("engine.maxSubworkflowDepth must not be negative"))
	}
	return errors.Join(errs...)
}

func (c ExpressionConfig) Validate() error {
	var errs []error
	errs = appendPositive(errs, "expression.evalTimeout", int64(c.EvalTimeout))
	errs = appendPositive(errs, "expression.maxTemplateLength", int64(c.MaxTemplateLength))
	errs = appendPositive(errs, "expression.maxOutputBytes", int64(c.MaxOutputBytes))
	errs = appendPositive(errs, "expression.maxCallStackDepth", int64(c.MaxCallStackDepth))
	errs = appendPositive(errs, "expression.programCacheSize", int64(c.ProgramCacheSize))
	for _, name := range c.EnvAllowList {
		if name == "" {
			errs = append(errs, errors.New("expression.envAllowList must not contain empty names"))
			break
		}
	}
	return errors.Join(errs...)
}

func (c LogConfig) Validate() error {
	var errs []error
	if !slices.Contains([]string{LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError}, c.Level) {
		errs = append(errs, fmt.Errorf("log.level %q is not one of debug, info, warn, error", c.Level))
	}
	if !slices.Contains([]string{LogFormatJSON, LogFormatText}, c.Format) {
		errs = append(errs, fmt.Errorf("log.format %q is not one of json, text", c.Format))
	}
	return errors.Join(errs...)
}

func appendPositive(errs []error, name string, v int64) []error {
	if v <= 0 {
		return append(errs, fmt.Errorf("%s must be greater than zero", name))
	}
	return errs
}
