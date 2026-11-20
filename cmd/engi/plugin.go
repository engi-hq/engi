package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/engi-hq/engi/internal/app"
	"github.com/engi-hq/engi/internal/config"
)

const pluginUsage = `usage: engi plugin <command> [arguments] [--config file]

commands:
  install <dir> [--force]          check the plugin, copy it into plugins.dir and make it active
  list                             installed plugins, their versions and the active one
  use <name> <version>             make another installed version active
  remove <name> [--version v]      remove one version, or the whole plugin

Changes take effect at the next server start.
`

func pluginCommand(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, pluginUsage)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("plugin "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String(flagConfig, "", "path to a YAML config file")
	force := fs.Bool("force", false, "replace an installed copy of the same version (install)")
	version := fs.String("version", "", "the version to remove (remove)")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return exitUsage
	}
	want := map[string]int{"install": 1, "list": 0, "use": 2, "remove": 1}
	n, known := want[sub]
	if !known {
		fmt.Fprintf(stderr, "engi plugin: unknown command %q\n\n%s", sub, pluginUsage)
		return exitUsage
	}
	if len(pos) != n {
		fmt.Fprintf(stderr, "engi plugin %s: expected %d argument(s)\n\n%s", sub, n, pluginUsage)
		return exitUsage
	}
	m, err := newPluginManager(*cfgPath, environ, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	switch sub {
	case "install":
		s, err := m.Install(ctx, pos[0], *force)
		if err != nil {
			fmt.Fprintf(stderr, "engi: installing plugin: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "installed %s %s (%s): %s\nrestart the server to load it\n", s.Name, s.Version, s.Transport, strings.Join(s.Nodes, ", "))
	case "list":
		list, err := m.List()
		if err != nil {
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
		printPlugins(stdout, list)
	case "use":
		if err := m.Use(pos[0], pos[1]); err != nil {
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "%s %s is active; restart the server to load it\n", pos[0], pos[1])
	case "remove":
		if err := m.Remove(pos[0], *version); err != nil {
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "removed %s %s\n", pos[0], *version)
	}
	return exitOK
}

func printPlugins(w io.Writer, list []app.InstalledPlugin) {
	if len(list) == 0 {
		fmt.Fprintln(w, "no plugins installed")
		return
	}
	for _, p := range list {
		versions := make([]string, len(p.Versions))
		for i, v := range p.Versions {
			if v == p.Active {
				v = "*" + v
			}
			versions[i] = v
		}
		fmt.Fprintf(w, "%s\t%s\n", p.Name, strings.Join(versions, " "))
	}
}

func newPluginManager(configPath string, environ []string, stderr io.Writer) (*app.PluginManager, error) {
	var args []string
	if configPath != "" {
		args = []string{"--" + flagConfig, configPath}
	}
	cfg, err := config.Load(config.LoadOptions{Args: args, Environ: environ})
	if err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	m, err := app.NewPluginManager(cfg, appOptions(environ, stderr))
	if err != nil {
		return nil, fmt.Errorf("starting engi: %w", err)
	}
	return m, nil
}
