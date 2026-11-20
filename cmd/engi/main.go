// Command engi is the Engi workflow automation platform.
//
// Subcommands: run, validate, version. The standard library flag package is
// enough for this small surface; a CLI framework earns its place once there
// are many subcommands.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is injected at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Environ())
	stop()
	os.Exit(code)
}
