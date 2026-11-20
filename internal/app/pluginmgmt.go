package app

import (
	"context"
	"fmt"
	"os"

	"github.com/engi-hq/engi/internal/adapter/plugin/fsstore"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// InstalledPlugin is one plugin in the plugins directory.
type InstalledPlugin struct {
	Name     string
	Active   string
	Versions []string
}

// PluginManager installs, lists, switches and removes plugins in
// plugins.dir (the `engi plugin` commands). It needs no database.
type PluginManager struct {
	env   pluginEnv
	store *fsstore.Store
}

// NewPluginManager builds the manager from a validated configuration.
func NewPluginManager(cfg config.Config, opts Options) (*PluginManager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	if opts.Logger == nil {
		out := opts.LogOutput
		if out == nil {
			out = os.Stderr
		}
		logger, err := NewLogger(cfg.Log, out)
		if err != nil {
			return nil, err
		}
		opts.Logger = logger
	}
	env := pluginEnv{cfg: cfg.Plugins, version: opts.Version, lookupEnv: opts.LookupEnv, readFile: opts.ReadFile, logger: opts.Logger}
	return &PluginManager{env: env, store: fsstore.New(cfg.Plugins.Dir)}, nil
}

// Install copies the plugin directory src into plugins.dir and makes it the
// active version, after starting the copy once to check what it declares: a
// plugin that only works from its source directory is refused. replace
// overwrites an installed copy of the same version.
func (m *PluginManager) Install(ctx context.Context, src string, replace bool) (domain.PluginStatus, error) {
	manifest, err := fsstore.ReadManifest(src)
	if err != nil {
		return domain.PluginStatus{}, err
	}
	var res pluginproto.InitializeResult
	check := func(dir string) error {
		caller, r, err := startPlugin(ctx, m.env, fsstore.Plugin{Manifest: manifest, Dir: dir})
		if err != nil {
			return err
		}
		res = r
		if err := caller.Close(ctx); err != nil {
			return fmt.Errorf("stopping plugin %s after the check: %w", manifest.Name, err)
		}
		return nil
	}
	if _, err := m.store.Install(src, manifest, replace, check); err != nil {
		return domain.PluginStatus{}, err
	}
	return domain.PluginStatus{
		Name: manifest.Name, Version: manifest.Version, Transport: transportOf(manifest),
		Nodes: nodeTypes(res), State: domain.PluginLoaded,
	}, nil
}

// List returns every installed plugin.
func (m *PluginManager) List() ([]InstalledPlugin, error) {
	all, err := m.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]InstalledPlugin, len(all))
	for i, p := range all {
		out[i] = InstalledPlugin(p)
	}
	return out, nil
}

// Use makes an installed version active (upgrade or rollback).
func (m *PluginManager) Use(name, version string) error { return m.store.Use(name, version) }

// Remove deletes one version, or the whole plugin when version is empty.
func (m *PluginManager) Remove(name, version string) error { return m.store.Remove(name, version) }
