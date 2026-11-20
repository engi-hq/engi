// Command nodedocs regenerates the Node Reference tables in docs/nodes from
// the node descriptors. It is a development tool and is not part of the engi
// binary.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/engi-hq/engi/internal/app"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/credtype/builtin"
	"github.com/engi-hq/engi/internal/tools/nodedocs"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

type clock struct{}

func (clock) Now() time.Time { return time.Time{} }

// load builds the real node and credential-type registries with default
// configuration. Nothing connects to a network or database.
func load() ([]nodesdk.Descriptor, []nodesdk.CredentialType, error) {
	reg, err := app.NewRegistry(config.Default().Nodes, clock{})
	if err != nil {
		return nil, nil, fmt.Errorf("building node registry: %w", err)
	}
	return reg.Descriptors(), builtin.Types(), nil
}

// run returns the pages that differ from what the descriptors produce.
func run(dir string) (map[string]string, error) {
	descs, creds, err := load()
	if err != nil {
		return nil, err
	}
	return nodedocs.Sync(dir, descs, creds)
}

func main() {
	dir := flag.String("dir", "docs/nodes", "directory holding the node reference pages")
	check := flag.Bool("check", false, "fail instead of writing when pages are stale")
	flag.Parse()
	changed, err := run(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nodedocs:", err)
		os.Exit(1)
	}
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	if *check {
		if len(files) > 0 {
			fmt.Fprintf(os.Stderr, "nodedocs: stale pages (run `make docs-nodes`): %v\n", files)
			os.Exit(1)
		}
		return
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte(changed[f]), 0o644); err != nil { //nolint:gosec // docs are world-readable
			fmt.Fprintln(os.Stderr, "nodedocs:", err)
			os.Exit(1)
		}
		fmt.Println("updated", f)
	}
}
