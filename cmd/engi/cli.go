package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/app"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
)

const usage = `usage: engi <command> [arguments]

commands:
  run <workflow.json> [--input items.json] [--start node] [--persist] [--config file]
            run a workflow and print the execution result as JSON;
            --persist stores the workflow and execution in the SQLite database
            (store.path) and runs them through the durable queue and a worker
  validate <workflow.json> [--config file]
            check a workflow against the registered node types
  serve [--roles api,webhook,trigger,worker] [--config file] [--server-addr :8080 ...]
            run the server: REST API, workers and probes. Needs a credential
            encryption key (ENGI_ENCRYPTION_KEYS, ENGI_ENCRYPTION_ACTIVE_KEY_ID).
            Every setting of docs/11 has a flag; SIGINT/SIGTERM stop it gracefully
  migrate up|down [--config file]
            apply all pending database migrations, or revert the latest one
  plugin install|list|use|remove ...
            manage any-language plugins in plugins.dir (see engi plugin)
  healthcheck [/path]
            exit 0 when the local server answers 2xx on /path (default /readyz);
            for container HEALTHCHECKs, as the image has no shell or curl
  version   print the engi version
`

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	flagConfig  = "config"
)

// run executes the CLI with the process environment and returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	return execute(context.Background(), args, stdout, stderr, os.Environ())
}

// execute dispatches a subcommand. environ is the ENGI_* source for the config.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "run":
		return runCommand(ctx, args[1:], stdout, stderr, environ)
	case "validate":
		return validateCommand(ctx, args[1:], stdout, stderr, environ)
	case "serve":
		return serveCommand(ctx, args[1:], stderr, environ)
	case "migrate":
		return migrateCommand(ctx, args[1:], stdout, stderr, environ)
	case "plugin":
		return pluginCommand(ctx, args[1:], stdout, stderr, environ)
	case "healthcheck":
		return healthcheckCommand(ctx, args[1:], stderr, environ)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "engi: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
}

// parseArgs parses flags interleaved with positional arguments, so both
// `engi run wf.json --input x` and `engi run --input x wf.json` work.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err //nolint:wrapcheck // flag errors are already descriptive
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

// newApp loads the configuration and builds the application. Logs go to stderr
// so stdout stays machine-readable.
func newApp(ctx context.Context, configPath string, environ []string, stderr io.Writer) (*app.App, error) {
	var args []string
	if configPath != "" {
		args = []string{"--" + flagConfig, configPath}
	}
	cfg, err := config.Load(config.LoadOptions{Args: args, Environ: environ})
	if err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	a, err := app.New(ctx, cfg, appOptions(environ, stderr))
	if err != nil {
		return nil, fmt.Errorf("starting engi: %w", err)
	}
	return a, nil
}

// appOptions are the process inputs of the application: logs on stderr, the
// version, and the environment the CLI was given.
func appOptions(environ []string, stderr io.Writer) app.Options {
	return app.Options{LogOutput: stderr, Version: version, LookupEnv: lookupIn(environ)}
}

// lookupIn looks a variable up in a "KEY=value" list, like os.LookupEnv.
func lookupIn(environ []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for i := len(environ) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(environ[i], "="); ok && k == name {
				return v, true
			}
		}
		return "", false
	}
}

// closePlugins stops the plugin processes a command started.
func closePlugins(ctx context.Context, a *app.App, stderr io.Writer) {
	if err := a.ClosePlugins(ctx); err != nil {
		fmt.Fprintf(stderr, "engi: stopping plugins: %v\n", err)
	}
}

func loadWorkflow(path string) (domain.Workflow, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is a command-line argument chosen by the user
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("reading workflow: %w", err)
	}
	wf, err := jsoncodec.DecodeWorkflow(raw)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("%s: %w", path, err)
	}
	return wf, nil
}

func validateCommand(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String(flagConfig, "", "path to a YAML config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprint(stderr, "engi validate: expected exactly one workflow file\n\n"+usage)
		return exitUsage
	}
	wf, err := loadWorkflow(pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	a, err := newApp(ctx, *cfgPath, environ, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	defer closePlugins(ctx, a, stderr)
	if err := a.Validate(wf); err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "ok: %q is valid (%d nodes, %d connections)\n", wf.Name, len(wf.Nodes), len(wf.Connections))
	return exitOK
}

func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String(flagConfig, "", "path to a YAML config file")
	inputPath := fs.String("input", "", "JSON file with the items for the start node")
	start := fs.String("start", "", "name of the node to start at (default: the workflow's trigger)")
	persist := fs.Bool("persist", false, "store the run in the database and go through the queue and a worker")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprint(stderr, "engi run: expected exactly one workflow file\n\n"+usage)
		return exitUsage
	}
	wf, err := loadWorkflow(pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	var items []domain.Item
	if *inputPath != "" {
		if items, err = loadItems(*inputPath); err != nil {
			fmt.Fprintf(stderr, "engi: %v\n", err)
			return exitFailure
		}
	}
	a, err := newApp(ctx, *cfgPath, environ, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	defer closePlugins(ctx, a, stderr)

	if *persist {
		return runPersisted(ctx, a, app.RunRequest{Workflow: wf, StartNode: *start, Items: items}, stdout, stderr)
	}
	state, err := a.Run(ctx, app.RunRequest{Workflow: wf, StartNode: *start, Items: items})
	if err != nil && state == nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	report := app.BuildReport(state)
	out, encErr := report.Encode()
	if encErr != nil {
		fmt.Fprintf(stderr, "engi: %v\n", encErr)
		return exitFailure
	}
	fmt.Fprintln(stdout, string(out))
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	switch state.Status {
	case domain.StatusSuccess, domain.StatusWaiting:
		return exitOK
	}
	if state.Error != nil {
		fmt.Fprintf(stderr, "engi: execution %s: %s\n", state.Status, state.Error.Message)
	}
	return exitFailure
}

// runPersisted runs through the persistent runtime and prints the same report.
func runPersisted(ctx context.Context, a *app.App, req app.RunRequest, stdout, stderr io.Writer) int {
	ex, err := a.RunPersisted(ctx, req)
	if ex.ID == "" {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	out, encErr := app.ReportFromExecution(ex).Encode()
	if encErr != nil {
		fmt.Fprintf(stderr, "engi: %v\n", encErr)
		return exitFailure
	}
	fmt.Fprintln(stdout, string(out))
	if err != nil {
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitFailure
	}
	switch ex.Status {
	case domain.StatusSuccess, domain.StatusWaiting:
		return exitOK
	}
	if ex.Error != nil {
		fmt.Fprintf(stderr, "engi: execution %s: %s\n", ex.Status, ex.Error.Message)
	}
	return exitFailure
}

// loadItems reads start items. The file holds an array; each element is either
// a full item ({"json": {...}}) or, as a shorthand, a plain object.
func loadItems(path string) ([]domain.Item, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is a command-line argument chosen by the user
	if err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	items, err := jsoncodec.DecodeItems(raw)
	if err == nil {
		return items, nil
	}
	wrapped, werr := wrapPlainObjects(raw)
	if werr != nil {
		return nil, fmt.Errorf("%s: %w", path, errors.Join(err, werr))
	}
	return wrapped, nil
}

// wrapPlainObjects converts [{"a":1}] into items {"json":{"a":1}}.
func wrapPlainObjects(raw []byte) ([]domain.Item, error) {
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, fmt.Errorf("input must be an array of objects: %w", err)
	}
	items := make([]domain.Item, len(objs))
	for i, o := range objs {
		items[i] = domain.Item{JSON: o}
	}
	return items, nil
}
