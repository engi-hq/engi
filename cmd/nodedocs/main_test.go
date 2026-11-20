package main

import (
	"path/filepath"
	"testing"
)

// TestDocsUpToDate fails when docs/nodes is stale or a node lacks a section.
func TestDocsUpToDate(t *testing.T) {
	changed, err := run(filepath.Join("..", "..", "docs", "nodes"))
	if err != nil {
		t.Fatal(err)
	}
	for f := range changed {
		t.Errorf("%s is stale: run `make docs-nodes`", f)
	}
}
