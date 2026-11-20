package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/engi-hq/engi/internal/config"
)

const (
	// defaultHealthPath is probed when no path is given: readiness, so a
	// container only counts as healthy once its dependencies are reachable.
	defaultHealthPath = "/readyz"
	// loopbackHost replaces an empty or wildcard listen host.
	loopbackHost = "127.0.0.1"
	// healthTimeoutFactor scales server.healthTimeout into the probe's own
	// deadline, leaving room for the server's dependency checks.
	healthTimeoutFactor = 2
)

// healthcheckCommand calls the local server's health endpoint and exits 0 when
// it answers 2xx. Container images have no shell or curl, so Docker and Swarm
// HEALTHCHECKs run `engi healthcheck [/path]`; the address comes from the
// usual server.addr setting.
func healthcheckCommand(ctx context.Context, args []string, stderr io.Writer, environ []string) int {
	path := defaultHealthPath
	if len(args) > 0 && strings.HasPrefix(args[0], "/") {
		path, args = args[0], args[1:]
	}
	cfg, err := config.Load(config.LoadOptions{Args: args, Environ: environ, FlagOutput: stderr})
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(stderr, "engi: %v\n", err)
		return exitUsage
	}
	host, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		fmt.Fprintf(stderr, "engi healthcheck: server.addr %q: %v\n", cfg.Server.Addr, err)
		return exitUsage
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = loopbackHost
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeoutFactor*cfg.Server.HealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+path, http.NoBody) //nolint:gosec // the target is the operator's own server.addr on loopback
	if err != nil {
		fmt.Fprintf(stderr, "engi healthcheck: %v\n", err)
		return exitUsage
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // see above
	if err != nil {
		fmt.Fprintf(stderr, "engi healthcheck: %v\n", err)
		return exitFailure
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fmt.Fprintf(stderr, "engi healthcheck: %s answered %s\n", path, resp.Status)
		return exitFailure
	}
	return exitOK
}
