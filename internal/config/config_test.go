package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"ok", func(*Config) {}, ""},
		{"zero max node runs", func(c *Config) { c.Engine.MaxNodeRuns = 0 }, "engine.maxNodeRuns"},
		{"negative depth", func(c *Config) { c.Engine.MaxSubworkflowDepth = -1 }, "engine.maxSubworkflowDepth"},
		{"zero depth is allowed", func(c *Config) { c.Engine.MaxSubworkflowDepth = 0 }, ""},
		{"zero eval timeout", func(c *Config) { c.Expression.EvalTimeout = 0 }, "expression.evalTimeout"},
		{"zero cache", func(c *Config) { c.Expression.ProgramCacheSize = 0 }, "expression.programCacheSize"},
		{"empty env name", func(c *Config) { c.Expression.EnvAllowList = []string{"A", ""} }, "envAllowList"},
		{"bad level", func(c *Config) { c.Log.Level = "loud" }, "log.level"},
		{"bad format", func(c *Config) { c.Log.Format = "xml" }, "log.format"},
		{"zero worker concurrency", func(c *Config) { c.Worker.Concurrency = 0 }, "worker.concurrency"},
		{"negative shutdown grace", func(c *Config) { c.Worker.ShutdownGrace = -1 }, "worker.shutdownGrace"},
		{"zero sync timeout", func(c *Config) { c.Worker.SyncRunTimeout = 0 }, "worker.syncRunTimeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	cfg := Default()
	cfg.Engine.MaxNodeRuns = 0
	cfg.Log.Level = "x"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "maxNodeRuns") || !strings.Contains(err.Error(), "log.level") {
		t.Fatalf("expected both problems, got %v", err)
	}
}

func fileReader(content string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return []byte(content), nil }
}

func TestLoad(t *testing.T) {
	const yamlFile = `
engine:
  maxNodeRuns: 50
  executionTimeout: 10m
expression:
  envAllowList: [FROM_FILE]
log:
  level: debug
`
	tests := []struct {
		name  string
		opts  LoadOptions
		check func(t *testing.T, c Config)
	}{
		{
			name: "defaults only",
			opts: LoadOptions{},
			check: func(t *testing.T, c Config) {
				if c.Engine.MaxNodeRuns != Default().Engine.MaxNodeRuns {
					t.Fatalf("maxNodeRuns = %d", c.Engine.MaxNodeRuns)
				}
			},
		},
		{
			name: "file overrides defaults and keeps untouched defaults",
			opts: LoadOptions{Args: []string{"--config=x.yaml"}, ReadFile: fileReader(yamlFile)},
			check: func(t *testing.T, c Config) {
				if c.Engine.MaxNodeRuns != 50 || c.Engine.ExecutionTimeout != 10*time.Minute {
					t.Fatalf("file values not applied: %+v", c.Engine)
				}
				if c.Engine.MaxSubworkflowDepth != Default().Engine.MaxSubworkflowDepth {
					t.Fatalf("untouched default lost: %+v", c.Engine)
				}
				if c.Log.Level != LogLevelDebug {
					t.Fatalf("log level = %q", c.Log.Level)
				}
			},
		},
		{
			name: "env overrides file, config path from env",
			opts: LoadOptions{
				Environ:  []string{"ENGI_CONFIG=x.yaml", "ENGI_ENGINE_MAX_NODE_RUNS=70", "UNRELATED=1", "ENGI_LOG_LEVEL=warn"},
				ReadFile: fileReader(yamlFile),
			},
			check: func(t *testing.T, c Config) {
				if c.Engine.MaxNodeRuns != 70 {
					t.Fatalf("env did not override file: %d", c.Engine.MaxNodeRuns)
				}
				if c.Log.Level != LogLevelWarn {
					t.Fatalf("log level = %q", c.Log.Level)
				}
			},
		},
		{
			name: "flags override env",
			opts: LoadOptions{
				Args:    []string{"--engine-max-node-runs=90", "--expression-env-allow-list=A, B"},
				Environ: []string{"ENGI_ENGINE_MAX_NODE_RUNS=70"},
			},
			check: func(t *testing.T, c Config) {
				if c.Engine.MaxNodeRuns != 90 {
					t.Fatalf("flag did not override env: %d", c.Engine.MaxNodeRuns)
				}
				if got := strings.Join(c.Expression.EnvAllowList, "|"); got != "A|B" {
					t.Fatalf("allow list = %q", got)
				}
			},
		},
		{
			name: "duration from env",
			opts: LoadOptions{Environ: []string{"ENGI_EXPRESSION_EVAL_TIMEOUT=250ms"}},
			check: func(t *testing.T, c Config) {
				if c.Expression.EvalTimeout != 250*time.Millisecond {
					t.Fatalf("eval timeout = %v", c.Expression.EvalTimeout)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(tt.opts)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		opts    LoadOptions
		wantErr string
	}{
		{"unknown yaml key", LoadOptions{Args: []string{"--config=x"}, ReadFile: fileReader("engine:\n  nope: 1\n")}, "nope"},
		{"unreadable file", LoadOptions{Args: []string{"--config=x"}, ReadFile: func(string) ([]byte, error) { return nil, errors.New("boom") }}, "boom"},
		{"bad env int", LoadOptions{Environ: []string{"ENGI_ENGINE_MAX_NODE_RUNS=abc"}}, "ENGI_ENGINE_MAX_NODE_RUNS"},
		{"bad flag duration", LoadOptions{Args: []string{"--engine-default-node-timeout=soon"}}, "engine-default-node-timeout"},
		{"unknown flag", LoadOptions{Args: []string{"--nope"}}, "nope"},
		{"invalid result", LoadOptions{Environ: []string{"ENGI_ENGINE_MAX_NODE_RUNS=0"}}, "engine.maxNodeRuns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestStoreAndQueueValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty path", func(c *Config) { c.Store.Path = "" }, "store.path"},
		{"bad journal", func(c *Config) { c.Store.JournalMode = "memory" }, "store.journalMode"},
		{"bad sync", func(c *Config) { c.Store.Synchronous = "x" }, "store.synchronous"},
		{"zero busy", func(c *Config) { c.Store.BusyTimeout = 0 }, "store.busyTimeout"},
		{"zero conns", func(c *Config) { c.Store.MaxOpenConns = 0 }, "store.maxOpenConns"},
		{"page cap below default", func(c *Config) { c.Store.MaxPageSize = 1 }, "store.maxPageSize"},
		{"heartbeat not shorter", func(c *Config) { c.Queue.HeartbeatInterval = c.Queue.LeaseDuration }, "queue.heartbeatInterval"},
		{"zero attempts", func(c *Config) { c.Queue.MaxAttempts = 0 }, "queue.maxAttempts"},
		{"negative retry delay", func(c *Config) { c.Queue.RetryDelay = -1 }, "queue.retryDelay"},
		{"zero retry delay ok", func(c *Config) { c.Queue.RetryDelay = 0 }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want mention of %q", err, tt.wantErr)
			}
		})
	}
}

func TestStoreAndQueueFromEnvAndFlags(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Args:    []string{"-queue-max-attempts", "7"},
		Environ: []string{"ENGI_STORE_PATH=/data/e.db", "ENGI_QUEUE_POLL_INTERVAL=250ms", "ENGI_QUEUE_MAX_ATTEMPTS=2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Store.Path != "/data/e.db" || cfg.Queue.PollInterval != 250*time.Millisecond || cfg.Queue.MaxAttempts != 7 {
		t.Errorf("got store %+v queue %+v", cfg.Store, cfg.Queue)
	}
}

func TestRuntimeAndArgon2Settings(t *testing.T) {
	cfg, err := Load(LoadOptions{Environ: []string{
		"ENGI_RUNTIME_MEMORY_LIMIT_MIB=256", "ENGI_RUNTIME_PPROF_ADDR=127.0.0.1:6060",
		"ENGI_AUTH_ARGON2_MAX_CONCURRENT=3", "ENGI_AUTH_ARGON2_RELEASE_MEMORY=false",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime.MemoryLimitMiB != 256 || cfg.Runtime.PprofAddr != "127.0.0.1:6060" ||
		cfg.Auth.Argon2.MaxConcurrent != 3 || cfg.Auth.Argon2.ReleaseMemory {
		t.Errorf("got %+v %+v", cfg.Runtime, cfg.Auth.Argon2)
	}
	d := Default()
	if d.Runtime.MemoryLimitMiB != 0 || d.Runtime.PprofAddr != "" {
		t.Errorf("diagnostics must be off by default: %+v", d.Runtime)
	}
	d.Runtime.MemoryLimitMiB = -1
	if d.Validate() == nil {
		t.Error("negative memory limit accepted")
	}
}

func TestS3SecretAccessKeyFile(t *testing.T) {
	read := func(path string) ([]byte, error) {
		if path != "/run/secrets/s3" {
			return nil, errors.New("no such file")
		}
		return []byte("from-file\n"), nil
	}
	cfg, err := Load(LoadOptions{ReadFile: read, Environ: []string{
		"ENGI_BLOB_DRIVER=s3", "ENGI_BLOB_S3_BUCKET=b", "ENGI_BLOB_S3_ACCESS_KEY_ID=id",
		"ENGI_BLOB_S3_SECRET_ACCESS_KEY_FILE=/run/secrets/s3",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Blob.S3.SecretAccessKey.Reveal(); got != "from-file" {
		t.Fatalf("secret = %q", got)
	}
}

func TestPythonCodeSettings(t *testing.T) {
	d := Default().Nodes.Code.Python
	if !d.Enabled || d.Interpreter != "python3" || len(d.AllowedModules) == 0 {
		t.Errorf("unexpected defaults: %+v", d)
	}
	cfg, err := Load(LoadOptions{
		Args: []string{"--nodes-code-python-interpreter=/opt/py/bin/python"},
		Environ: []string{
			"ENGI_NODES_CODE_PYTHON_ENABLED=false", "ENGI_NODES_CODE_PYTHON_ALLOWED_MODULES=json, math",
			"ENGI_NODES_CODE_PYTHON_INTERPRETER=ignored",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Nodes.Code.Python
	if p.Enabled || p.Interpreter != "/opt/py/bin/python" || strings.Join(p.AllowedModules, "|") != "json|math" {
		t.Errorf("got %+v", p)
	}

	bad := Default()
	bad.Nodes.Code.Python.Interpreter = " "
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "nodes.code.python.interpreter") {
		t.Errorf("empty interpreter: %v", err)
	}
	bad.Nodes.Code.Python.Enabled = false
	if err := bad.Validate(); err != nil {
		t.Errorf("disabled node needs no interpreter: %v", err)
	}
	bad = Default()
	bad.Nodes.Code.Python.AllowedModules = []string{"json", ""}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "allowedModules") {
		t.Errorf("empty module: %v", err)
	}
}

func TestS3KeysAreSetTogetherOrLeftForTheDefaultChain(t *testing.T) {
	base := []string{"ENGI_BLOB_DRIVER=s3", "ENGI_BLOB_S3_BUCKET=b", "ENGI_BLOB_S3_REGION=eu-west-1"}
	if _, err := Load(LoadOptions{Environ: base}); err != nil {
		t.Fatalf("no keys (default credential chain) must be valid: %v", err)
	}
	_, err := Load(LoadOptions{Environ: append(base, "ENGI_BLOB_S3_ACCESS_KEY_ID=id")})
	if err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("one key alone: %v", err)
	}
}

func TestUnknownEnv(t *testing.T) {
	withAllow := Default()
	withAllow.Expression.EnvAllowList = []string{"ENGI_CUSTOM_FOR_EXPRESSIONS"}
	tests := []struct {
		name    string
		cfg     Config
		environ []string
		want    []string
	}{
		{"no ENGI variables", Default(), []string{"PATH=/bin", "HOME=/root"}, nil},
		{"settings are known", Default(), []string{"ENGI_LOG_LEVEL=debug", "ENGI_KAFKA_PAUSE_ABOVE_DEPTH=10"}, nil},
		{"config file locator is known", Default(), []string{"ENGI_CONFIG=/etc/engi.yaml"}, nil},
		{"compose variables are known", Default(), []string{"ENGI_IMAGE=engi:dev", "ENGI_PORT=8080"}, nil},
		{"typo is reported", Default(), []string{"ENGI_KAFKA_PAUSE_ABOVE_DEPT=10"}, []string{"ENGI_KAFKA_PAUSE_ABOVE_DEPT"}},
		{"kafka setting is known", Default(), []string{"ENGI_KAFKA_BATCH_MAX_WAIT=30s"}, nil},
		{"expression allow-list is known", withAllow, []string{"ENGI_CUSTOM_FOR_EXPRESSIONS=x"}, nil},
		{"only ENGI prefix is checked", Default(), []string{"ENGI_NOPE=1", "ENGINE_NOPE=2", "XENGI_NOPE=3"}, []string{"ENGI_NOPE"}},
		{"sorted and deduplicated", Default(), []string{"ENGI_ZZZ=1", "ENGI_AAA=1", "ENGI_AAA=2"}, []string{"ENGI_AAA", "ENGI_ZZZ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnknownEnv(tt.environ, tt.cfg)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("UnknownEnv = %v, want %v", got, tt.want)
			}
		})
	}
}
