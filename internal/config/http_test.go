package config

import (
	"strings"
	"testing"
	"time"
)

func TestHTTPClientAndNodesValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"zero timeout", func(c *Config) { c.HTTPClient.Timeout = 0 }, "httpClient.timeout"},
		{"zero dial timeout", func(c *Config) { c.HTTPClient.DialTimeout = 0 }, "httpClient.dialTimeout"},
		{"zero max response", func(c *Config) { c.HTTPClient.MaxResponseBytes = 0 }, "httpClient.maxResponseBytes"},
		{"negative redirects", func(c *Config) { c.HTTPClient.MaxRedirects = -1 }, "httpClient.maxRedirects"},
		{"zero redirects allowed", func(c *Config) { c.HTTPClient.MaxRedirects = 0 }, ""},
		{"negative idle conns", func(c *Config) { c.HTTPClient.MaxIdleConns = -1 }, "httpClient.maxIdleConns"},
		{"relative proxy", func(c *Config) { c.HTTPClient.ProxyURL = "proxy:3128" }, "httpClient.proxyUrl"},
		{"proxy ok", func(c *Config) { c.HTTPClient.ProxyURL = "http://proxy:3128" }, ""},
		{"zero code timeout", func(c *Config) { c.Nodes.Code.Timeout = 0 }, "nodes.code.timeout"},
		{"zero code stack", func(c *Config) { c.Nodes.Code.MaxCallStackDepth = 0 }, "nodes.code.maxCallStackDepth"},
		{"zero code output", func(c *Config) { c.Nodes.Code.MaxOutputBytes = 0 }, "nodes.code.maxOutputBytes"},
		{"zero switch rules", func(c *Config) { c.Nodes.Switch.MaxRules = 0 }, "nodes.switch.maxRules"},
		{"zero db open conns", func(c *Config) { c.Nodes.Database.MaxOpenConns = 0 }, "nodes.database.maxOpenConns"},
		{"zero db connect timeout", func(c *Config) { c.Nodes.Database.ConnectTimeout = 0 }, "nodes.database.connectTimeout"},
		{"db idle above open", func(c *Config) { c.Nodes.Database.MaxIdleConns = c.Nodes.Database.MaxOpenConns + 1 }, "nodes.database.maxIdleConns"},
		{"merge needs two inputs", func(c *Config) { c.Nodes.Merge.MaxInputs = 1 }, "nodes.merge.maxInputs"},
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

func TestLoadHTTPClientAndNodes(t *testing.T) {
	yamlDoc := "httpClient:\n  timeout: 5s\n  userAgent: from-file\n  maxResponseBytes: 2048\nnodes:\n  switch:\n    maxRules: 4\n"
	cfg, err := Load(LoadOptions{
		Args:     []string{"--config", "engi.yaml", "--http-client-max-redirects", "2"},
		Environ:  []string{"ENGI_HTTP_CLIENT_USER_AGENT=from-env", "ENGI_NODES_MERGE_MAX_INPUTS=3", "ENGI_HTTP_CLIENT_TLS_INSECURE_SKIP_VERIFY=true", "ENGI_NODES_CODE_TIMEOUT=3s"},
		ReadFile: fileReader(yamlDoc),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.HTTPClient
	if h.Timeout != 5*time.Second || h.MaxResponseBytes != 2048 {
		t.Errorf("file values lost: %+v", h)
	}
	if h.UserAgent != "from-env" {
		t.Errorf("environment must win over the file, got %q", h.UserAgent)
	}
	if h.MaxRedirects != 2 || !h.TLSInsecureSkipVerify {
		t.Errorf("flag/bool env not applied: %+v", h)
	}
	if h.DialTimeout != Default().HTTPClient.DialTimeout {
		t.Errorf("untouched value must keep its default")
	}
	n := cfg.Nodes
	if n.Switch.MaxRules != 4 || n.Merge.MaxInputs != 3 || n.Code.Timeout != 3*time.Second {
		t.Errorf("nodes = %+v", n)
	}
}

func TestLoadRejectsBadHTTPValues(t *testing.T) {
	for _, env := range []string{
		"ENGI_HTTP_CLIENT_TIMEOUT=soon", "ENGI_HTTP_CLIENT_MAX_REDIRECTS=many", "ENGI_HTTP_CLIENT_TLS_INSECURE_SKIP_VERIFY=maybe",
		"ENGI_HTTP_CLIENT_MAX_RESPONSE_BYTES=big", "ENGI_NODES_SWITCH_MAX_RULES=x",
	} {
		if _, err := Load(LoadOptions{Environ: []string{env}}); err == nil {
			t.Errorf("%s should be rejected", env)
		}
	}
}
