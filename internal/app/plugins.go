package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/engi-hq/engi/internal/adapter/plugin/fsstore"
	"github.com/engi-hq/engi/internal/adapter/plugin/httprpc"
	"github.com/engi-hq/engi/internal/adapter/plugin/pluginnode"
	"github.com/engi-hq/engi/internal/adapter/plugin/stdio"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/pkg/nodesdk"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// hostName identifies Engi to plugins in initialize.
const hostName = "engi"

// Transports of a plugin status.
const (
	transportStdio = "stdio"
	transportHTTP  = "http"
)

// pluginEnv is what the plugin host needs from the process environment.
type pluginEnv struct {
	cfg       config.PluginsConfig
	version   string
	lookupEnv func(string) (string, bool)
	readFile  func(string) ([]byte, error)
	logger    *slog.Logger
}

// pluginCaller is a started plugin: it runs calls and must be closed.
type pluginCaller interface {
	pluginnode.Caller
	Close(ctx context.Context) error
}

// pluginHost owns the plugins the server loaded.
type pluginHost struct {
	statuses []domain.PluginStatus
	callers  []pluginCaller
}

// loadPlugins starts the active version of every installed plugin and
// registers its node types. A plugin that fails is logged and reported in its
// status; it never stops the server from starting.
func loadPlugins(ctx context.Context, env pluginEnv, reg *registry.Registry) *pluginHost {
	h := &pluginHost{}
	if !env.cfg.Enabled {
		return h
	}
	plugins, failed, err := fsstore.New(env.cfg.Dir).Active()
	if err != nil {
		env.logger.Error("reading installed plugins", "dir", env.cfg.Dir, "error", err)
		return h
	}
	for _, f := range failed {
		h.fail(env.logger, domain.PluginStatus{Name: f.Name, Version: f.Version}, f.Err)
	}
	for _, p := range plugins {
		status := domain.PluginStatus{Name: p.Manifest.Name, Version: p.Manifest.Version, Transport: transportOf(p.Manifest), Nodes: p.Manifest.Nodes}
		caller, res, err := startPlugin(ctx, env, p)
		if err == nil {
			err = registerPlugin(reg, caller, res, env.cfg)
			if err != nil {
				_ = caller.Close(ctx)
			}
		}
		if err != nil {
			h.fail(env.logger, status, err)
			continue
		}
		status.State, status.Nodes = domain.PluginLoaded, nodeTypes(res)
		h.statuses = append(h.statuses, status)
		h.callers = append(h.callers, caller)
		env.logger.Info("plugin loaded", "plugin", status.Name, "version", status.Version, "nodes", status.Nodes)
	}
	return h
}

func (h *pluginHost) fail(log *slog.Logger, s domain.PluginStatus, err error) {
	s.State, s.Error = domain.PluginFailed, err.Error()
	h.statuses = append(h.statuses, s)
	log.Error("plugin not loaded", "plugin", s.Name, "version", s.Version, "error", err)
}

// close stops every plugin process.
func (h *pluginHost) close(ctx context.Context) error {
	var errs []error
	for _, c := range h.callers {
		errs = append(errs, c.Close(ctx))
	}
	h.callers = nil
	return errors.Join(errs...)
}

// registerPlugin adds every node type of a started plugin to the registry. A
// type that is already registered refuses the whole plugin, so a plugin is
// either fully available or not at all.
func registerPlugin(reg *registry.Registry, caller pluginnode.Caller, res pluginproto.InitializeResult, cfg config.PluginsConfig) error {
	for _, n := range res.Nodes {
		if _, err := reg.Descriptor(n.Type, n.Version); err == nil {
			return fmt.Errorf("node type %s version %d is already registered", n.Type, n.Version)
		}
	}
	opts := pluginnode.Options{MaxBlobBytes: int64(cfg.MaxBlobBytes)}
	for _, n := range res.Nodes {
		d := n.ToDescriptor()
		if err := reg.Register(func() nodesdk.Node { return pluginnode.New(d, caller, opts) }); err != nil {
			return fmt.Errorf("registering %s: %w", d.Type, err)
		}
	}
	return nil
}

// startPlugin starts a plugin over its transport and returns its descriptors.
func startPlugin(ctx context.Context, env pluginEnv, p fsstore.Plugin) (pluginCaller, pluginproto.InitializeResult, error) {
	m, cfg := p.Manifest, env.cfg
	host := pluginproto.Info{Name: hostName, Version: env.version}
	if m.Runtime.IsHTTP() {
		token, err := readToken(env, p)
		if err != nil {
			return nil, pluginproto.InitializeResult{}, err
		}
		c := httprpc.New(httprpc.Config{
			Name: m.Name, URL: m.Runtime.URL, Token: token, MaxMessageBytes: int64(cfg.MaxMessageBytes),
			CallTimeout: cfg.CallTimeout, HostInfo: host, Declared: m.Nodes,
		}, &http.Client{Timeout: cfg.CallTimeout})
		sctx, cancel := context.WithTimeout(ctx, cfg.StartTimeout)
		defer cancel()
		res, err := c.Start(sctx)
		if err != nil {
			return nil, res, fmt.Errorf("starting plugin %s: %w", m.Name, err)
		}
		return c, res, nil
	}
	s := stdio.New(stdio.Config{
		Name: m.Name, Dir: p.Dir, Command: m.Runtime.Command, Env: pluginEnviron(env, m),
		HostInfo: host, Declared: m.Nodes,
		MaxMessageBytes: cfg.MaxMessageBytes, MaxConcurrentCalls: cfg.MaxConcurrentCalls,
		StartTimeout: cfg.StartTimeout, CallTimeout: cfg.CallTimeout, ShutdownGrace: cfg.ShutdownGrace,
		IdleTimeout: cfg.IdleTimeout, RestartBackoff: cfg.RestartBackoff, MaxLogLineBytes: cfg.MaxLogLineBytes,
		StartErrorLines: cfg.StartErrorLines,
	}, env.logger)
	res, err := s.Start(ctx)
	if err != nil {
		_ = s.Close(ctx)
		return nil, res, fmt.Errorf("starting plugin %s: %w", m.Name, err)
	}
	return s, res, nil
}

// pluginEnviron is a stdio plugin's whole environment: the allow-listed
// variables of the server, then the manifest's own. Engi's settings and
// secrets (ENGI_*) are never passed on.
func pluginEnviron(env pluginEnv, m pluginproto.Manifest) []string {
	vars := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, seen := vars[k]; !seen {
			order = append(order, k)
		}
		vars[k] = v
	}
	for _, k := range env.cfg.InheritEnv {
		if v, ok := env.lookupEnv(k); ok && !isEngiSetting(k) {
			set(k, v)
		}
	}
	for k, v := range m.Runtime.Env {
		if !isEngiSetting(k) {
			set(k, v)
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// engiSettingPrefix starts the environment variables that configure Engi.
const engiSettingPrefix = "ENGI_"

func isEngiSetting(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), engiSettingPrefix)
}

func readToken(env pluginEnv, p fsstore.Plugin) (string, error) {
	path := p.Manifest.Runtime.TokenFile
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Dir, path)
	}
	b, err := env.readFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the token of plugin %s: %w", p.Manifest.Name, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func transportOf(m pluginproto.Manifest) string {
	if m.Runtime.IsHTTP() {
		return transportHTTP
	}
	return transportStdio
}

func nodeTypes(res pluginproto.InitializeResult) []string {
	out := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		out = append(out, n.Type)
	}
	return out
}
