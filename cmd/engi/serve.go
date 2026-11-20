package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/engi-hq/engi/internal/adapter/http/ui"
	"github.com/engi-hq/engi/internal/app"
	"github.com/engi-hq/engi/internal/config"
)

// serveCommand runs the server until ctx is canceled by SIGINT or SIGTERM.
// Every argument is a configuration flag (--roles, --server-addr, --config, ...),
// handled by config.Load.
func serveCommand(ctx context.Context, args []string, stderr io.Writer, environ []string) int {
	cfg, err := config.Load(config.LoadOptions{Args: args, Environ: environ, FlagOutput: stderr})
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitUsage
	}
	if err := warnUnknownEnv(cfg, environ, stderr); err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	// Fail fast, before any file is opened, when credentials cannot be encrypted.
	if err := cfg.Credentials.RequireKeyring(); err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	a, err := app.New(ctx, cfg, appOptions(environ, stderr))
	if err != nil {
		fmt.Fprintf(stderr, "engi: starting engi: %v\n", err)
		return exitFailure
	}
	stopDiag, err := app.ApplyRuntime(cfg.Runtime, a.Logger)
	if err != nil {
		_ = a.CloseCredentials()
		closePlugins(ctx, a, stderr)
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	defer func() { _ = stopDiag(context.WithoutCancel(ctx)) }()
	var opts app.ServerOptions
	if cfg.Server.UI.Enabled {
		if opts.UI, err = ui.New(ui.Options{ContentSecurityPolicy: cfg.Server.UI.ContentSecurityPolicy}); err != nil {
			_ = a.CloseCredentials()
			closePlugins(ctx, a, stderr)
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
	}
	srv, err := a.NewServer(ctx, opts)
	if err != nil {
		_ = a.CloseCredentials()
		closePlugins(ctx, a, stderr)
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// warnUnknownEnv logs one WARN naming the ENGI_* variables no setting reads.
// Only names are logged, never values, and the server still starts.
func warnUnknownEnv(cfg config.Config, environ []string, stderr io.Writer) error {
	unknown := config.UnknownEnv(environ, cfg)
	if len(unknown) == 0 {
		return nil
	}
	logger, err := app.NewLogger(cfg.Log, stderr)
	if err != nil {
		return fmt.Errorf("logging unknown environment variables: %w", err)
	}
	logger.Warn("ignoring unknown ENGI_* environment variables", "names", unknown,
		"hint", "check docs/11 for the setting names")
	return nil
}

const (
	migrateUp   = "up"
	migrateDown = "down"
)

// migrateCommand applies or reverts database migrations and exits.
func migrateCommand(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 || (args[0] != migrateUp && args[0] != migrateDown) {
		fmt.Fprint(stderr, "engi migrate: expected `up` or `down`\n\n"+usage)
		return exitUsage
	}
	cfg, err := config.Load(config.LoadOptions{Args: args[1:], Environ: environ, FlagOutput: stderr})
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitUsage
	}
	if args[0] == migrateUp {
		if err := app.MigrateUp(ctx, cfg); err != nil {
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
		fmt.Fprintln(stdout, "migrations applied")
		return exitOK
	}
	v, err := app.MigrateDown(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	if v == 0 {
		fmt.Fprintln(stdout, "nothing to revert")
		return exitOK
	}
	fmt.Fprintf(stdout, "reverted migration %d\n", v)
	return exitOK
}
