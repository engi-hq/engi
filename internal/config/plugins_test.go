package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPluginsDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Args:    []string{"--plugins-dir", "/opt/engi/plugins"},
		Environ: []string{"ENGI_PLUGINS_INHERIT_ENV=PATH, LANG", "ENGI_PLUGINS_IDLE_TIMEOUT=2m"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Plugins
	if !p.Enabled || p.Dir != "/opt/engi/plugins" || p.IdleTimeout != 2*time.Minute || !slices.Equal(p.InheritEnv, []string{"PATH", "LANG"}) {
		t.Fatalf("plugins = %+v", p)
	}
}

func TestPluginsStartErrorLines(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    int
	}{
		{"default", nil, 20},
		{"environment", []string{"ENGI_PLUGINS_START_ERROR_LINES=5"}, 5},
		{"zero keeps none", []string{"ENGI_PLUGINS_START_ERROR_LINES=0"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(LoadOptions{Environ: tt.environ})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Plugins.StartErrorLines != tt.want {
				t.Errorf("StartErrorLines = %d, want %d", cfg.Plugins.StartErrorLines, tt.want)
			}
		})
	}
}

func TestPluginsValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PluginsConfig)
		want   string
	}{
		{"empty dir", func(p *PluginsConfig) { p.Dir = "" }, "plugins.dir"},
		{"negative idle", func(p *PluginsConfig) { p.IdleTimeout = -time.Second }, "idleTimeout"},
		{"blob larger than message", func(p *PluginsConfig) { p.MaxBlobBytes = p.MaxMessageBytes }, "base64"},
		{"engi secret", func(p *PluginsConfig) { p.InheritEnv = []string{"PATH", "engi_encryption_keys"} }, "never passed"},
		{"zero calls", func(p *PluginsConfig) { p.MaxConcurrentCalls = 0 }, "maxConcurrentCalls"},
		{"negative start error lines", func(p *PluginsConfig) { p.StartErrorLines = -1 }, "startErrorLines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := defaultPlugins()
			tt.mutate(&p)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	disabled := defaultPlugins()
	disabled.Enabled, disabled.Dir = false, ""
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled plugins need no dir: %v", err)
	}
}
