package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	envPrefix = "ENGI_"
	// configFileEnv and configFileFlag locate the optional YAML file.
	configFileEnv  = "ENGI_CONFIG"
	configFileFlag = "config"
)

// LoadOptions are the inputs of Load. They are injected so loading is testable
// and never reads process globals itself.
type LoadOptions struct {
	// Args are the command-line arguments, without the program name.
	Args []string
	// Environ is the environment in "KEY=value" form (os.Environ()).
	Environ []string
	// ReadFile reads the config file. Defaults to os.ReadFile.
	ReadFile func(path string) ([]byte, error)
	// FlagOutput receives flag usage and parse errors. Defaults to io.Discard.
	FlagOutput io.Writer
}

// Load builds the configuration: defaults, then the optional YAML file, then
// ENGI_* environment variables, then flags. The result is validated.
func Load(opts LoadOptions) (Config, error) {
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	if opts.FlagOutput == nil {
		opts.FlagOutput = io.Discard
	}
	env := parseEnviron(opts.Environ)

	flagValues, path, err := parseFlags(opts.Args, opts.FlagOutput)
	if err != nil {
		return Config{}, err
	}
	if path == "" {
		path = env[configFileEnv]
	}

	cfg := Default()
	if path != "" {
		if err := applyFile(&cfg, path, opts.ReadFile); err != nil {
			return Config{}, err
		}
	}
	if err := applySettings(&cfg, env, func(s setting) string { return s.env }, "environment variable"); err != nil {
		return Config{}, err
	}
	if err := applySettings(&cfg, flagValues, func(s setting) string { return s.flag }, "flag"); err != nil {
		return Config{}, err
	}
	if err := cfg.Credentials.loadKeysFile(opts.ReadFile); err != nil {
		return Config{}, err
	}
	if err := cfg.Blob.S3.loadSecretFile(opts.ReadFile); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func applyFile(cfg *Config, path string, read func(string) ([]byte, error)) error {
	raw, err := read(path)
	if err != nil {
		return fmt.Errorf("reading config file %q: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parsing config file %q: %w", path, err)
	}
	return nil
}

// envOutsideSettings are ENGI_* variables that are valid without being a
// setting: the config file locator, and the image and port that docker compose
// reads from the same .env file (env_file passes them into the container too).
var envOutsideSettings = []string{configFileEnv, "ENGI_IMAGE", "ENGI_PORT"}

// UnknownEnv returns the sorted ENGI_* names in environ that nothing reads: no
// setting, no entry of envOutsideSettings and no name in cfg's expression
// allow-list. Only names are returned, never values. It is advisory: Load
// ignores unknown variables, so a typo does not stop the server.
func UnknownEnv(environ []string, cfg Config) []string {
	known := make(map[string]bool, len(envOutsideSettings))
	for _, s := range settings() {
		known[s.env] = true
	}
	for _, name := range envOutsideSettings {
		known[name] = true
	}
	for _, name := range cfg.Expression.EnvAllowList {
		known[name] = true
	}
	var unknown []string
	for name := range parseEnviron(environ) {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	return unknown
}

func parseEnviron(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, envPrefix) {
			env[k] = v
		}
	}
	return env
}

// parseFlags registers one flag per setting, records which were given and
// returns their raw values keyed by flag name, plus the config file path.
func parseFlags(args []string, out io.Writer) (given map[string]string, path string, err error) {
	fs := flag.NewFlagSet("engi", flag.ContinueOnError)
	fs.SetOutput(out)
	given = map[string]string{}
	for _, s := range settings() {
		if s.flag == "" {
			continue // environment-only setting
		}
		fs.Func(s.flag, s.usage, func(v string) error {
			given[s.flag] = v
			return nil
		})
	}
	pathFlag := fs.String(configFileFlag, "", "path to a YAML config file")
	if err := fs.Parse(args); err != nil {
		return nil, "", fmt.Errorf("parsing flags: %w", err)
	}
	return given, *pathFlag, nil
}

func applySettings(cfg *Config, raw map[string]string, key func(setting) string, kind string) error {
	for _, s := range settings() {
		k := key(s)
		v, ok := raw[k]
		if !ok {
			continue
		}
		if err := s.set(cfg, v); err != nil {
			return fmt.Errorf("%s %s: %w", kind, k, err)
		}
	}
	return nil
}

// setting binds one configuration field to its environment variable and flag.
type setting struct {
	env   string
	flag  string
	usage string
	set   func(*Config, string) error
}

// settings is the table of all settings reachable from env and flags.
func settings() []setting {
	return slices.Concat(coreSettings(), storeSettings(), blobSettings(), httpClientSettings(), nodeSettings(), emailSettings(), pollSettings(), chatSettings(), googleSettings(), airtableSettings(), aiSettings(), mcpSettings(), workerSettings(), credentialSettings(), serverSettings(), authSettings(), runtimeSettings(), observabilitySettings(), retentionSettings(), workflowSettings(), pluginSettings(), triggerSettings())
}

func coreSettings() []setting {
	return []setting{
		{"ENGI_ENGINE_MAX_NODE_RUNS", "engine-max-node-runs", "max runs of one node per execution",
			func(c *Config, v string) (err error) { c.Engine.MaxNodeRuns, err = strconv.Atoi(v); return }},
		{"ENGI_ENGINE_DEFAULT_NODE_TIMEOUT", "engine-default-node-timeout", "default per-node timeout",
			func(c *Config, v string) (err error) {
				c.Engine.DefaultNodeTimeout, err = time.ParseDuration(v)
				return
			}},
		{"ENGI_ENGINE_EXECUTION_TIMEOUT", "engine-execution-timeout", "default execution timeout",
			func(c *Config, v string) (err error) { c.Engine.ExecutionTimeout, err = time.ParseDuration(v); return }},
		{"ENGI_ENGINE_MAX_SUBWORKFLOW_DEPTH", "engine-max-subworkflow-depth", "max sub-workflow nesting",
			func(c *Config, v string) (err error) { c.Engine.MaxSubworkflowDepth, err = strconv.Atoi(v); return }},
		{"ENGI_ENGINE_MAX_RETRY_DELAY", "engine-max-retry-delay", "cap on the delay between retries",
			func(c *Config, v string) (err error) { c.Engine.MaxRetryDelay, err = time.ParseDuration(v); return }},
		{"ENGI_EXPRESSION_EVAL_TIMEOUT", "expression-eval-timeout", "max time of one expression evaluation",
			func(c *Config, v string) (err error) { c.Expression.EvalTimeout, err = time.ParseDuration(v); return }},
		{"ENGI_EXPRESSION_MAX_TEMPLATE_LENGTH", "expression-max-template-length", "max expression template bytes",
			func(c *Config, v string) (err error) { c.Expression.MaxTemplateLength, err = strconv.Atoi(v); return }},
		{"ENGI_EXPRESSION_MAX_OUTPUT_BYTES", "expression-max-output-bytes", "max serialized expression result bytes",
			func(c *Config, v string) (err error) { c.Expression.MaxOutputBytes, err = strconv.Atoi(v); return }},
		{"ENGI_EXPRESSION_MAX_CALL_STACK_DEPTH", "expression-max-call-stack-depth", "max expression recursion depth",
			func(c *Config, v string) (err error) { c.Expression.MaxCallStackDepth, err = strconv.Atoi(v); return }},
		{"ENGI_EXPRESSION_PROGRAM_CACHE_SIZE", "expression-program-cache-size", "compiled expression cache entries",
			func(c *Config, v string) (err error) { c.Expression.ProgramCacheSize, err = strconv.Atoi(v); return }},
		{"ENGI_EXPRESSION_ENV_ALLOW_LIST", "expression-env-allow-list", "comma-separated env vars visible as $env",
			func(c *Config, v string) error { c.Expression.EnvAllowList = splitList(v); return nil }},
		{"ENGI_LOG_LEVEL", "log-level", "log level: debug, info, warn, error",
			func(c *Config, v string) error { c.Log.Level = v; return nil }},
		{"ENGI_LOG_FORMAT", "log-format", "log format: json, text",
			func(c *Config, v string) error { c.Log.Format = v; return nil }},
	}
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseUint parses an unsigned decimal that must fit in bitSize bits.
func parseUint(v string, bitSize int) (uint64, error) {
	n, err := strconv.ParseUint(v, 10, bitSize)
	if err != nil {
		return 0, fmt.Errorf("parsing %q: %w", v, err)
	}
	return n, nil
}
