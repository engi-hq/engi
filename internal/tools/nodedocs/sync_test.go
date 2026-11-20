package nodedocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestSyncMissingMarker(t *testing.T) {
	dir := t.TempDir()
	page := "# P\n\n<!-- BEGIN GENERATED: credential-types -->\n<!-- END GENERATED: credential-types -->\n"
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(dir, []nodesdk.Descriptor{{Type: "x.y", Version: 1}}, nil)
	if err == nil || !strings.Contains(err.Error(), `"x.y" has no documentation section`) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncReplacesOnlyMarkedContent(t *testing.T) {
	dir := t.TempDir()
	page := "## X\nprose\n<!-- BEGIN GENERATED: x.y -->\nold\n<!-- END GENERATED: x.y -->\ntail\n<!-- BEGIN GENERATED: credential-types -->\n<!-- END GENERATED: credential-types -->\n"
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Sync(dir, []nodesdk.Descriptor{{Type: "x.y", Version: 1, DisplayName: "X"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out[filepath.Join(dir, "a.md")]
	if !strings.Contains(got, "prose\n<!-- BEGIN") || !strings.Contains(got, "-->\ntail\n") || strings.Contains(got, "old") {
		t.Fatalf("got:\n%s", got)
	}
}
