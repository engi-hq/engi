package fsstore_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/plugin/fsstore"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// pluginDir writes a plugin directory with a manifest for name and version.
func pluginDir(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "name: " + name + "\nversion: " + version + "\nruntime:\n  command: [./run.sh]\nnodes: [acme.x]\n"
	write(t, filepath.Join(dir, pluginproto.ManifestFile), manifest, 0o644)
	write(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(dir, "lib", "data.txt"), version, 0o644)
	return dir
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func install(t *testing.T, s *fsstore.Store, src string, replace bool) (fsstore.Plugin, error) {
	t.Helper()
	m, err := fsstore.ReadManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	return s.Install(src, m, replace, nil)
}

func TestInstallUseRemoveLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	s := fsstore.New(root)
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty store list = %v, %v", list, err)
	}

	p1, err := install(t, s, pluginDir(t, "acme", "1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(p1.Dir, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("executable bit lost: %v %v", info, err)
	}
	if _, err := install(t, s, pluginDir(t, "acme", "1.1.0"), false); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Active != "1.1.0" || strings.Join(list[0].Versions, ",") != "1.0.0,1.1.0" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if err := s.Use("acme", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	active, failed, err := s.Active()
	if err != nil || len(failed) != 0 || len(active) != 1 || active[0].Manifest.Version != "1.0.0" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	data, _ := os.ReadFile(filepath.Join(active[0].Dir, "lib", "data.txt"))
	if string(data) != "1.0.0" {
		t.Fatalf("active copy holds %q", data)
	}

	if err := s.Remove("acme", "1.0.0"); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("removing the active version: %v", err)
	}
	if err := s.Remove("acme", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.Use("acme", "1.1.0"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("use removed version: %v", err)
	}
	if err := s.Remove("acme", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("acme", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
}

func TestInstallSameVersionNeedsReplace(t *testing.T) {
	s := fsstore.New(filepath.Join(t.TempDir(), "plugins"))
	if _, err := install(t, s, pluginDir(t, "acme", "1.0.0"), false); err != nil {
		t.Fatal(err)
	}
	src := pluginDir(t, "acme", "1.0.0")
	write(t, filepath.Join(src, "lib", "data.txt"), "new", 0o644)
	if _, err := install(t, s, src, false); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("err = %v", err)
	}
	p, err := install(t, s, src, true)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(p.Dir, "lib", "data.txt")); string(data) != "new" {
		t.Fatalf("replace kept old content %q", data)
	}
}

func TestInstallRefusesLinksLeavingThePlugin(t *testing.T) {
	s := fsstore.New(filepath.Join(t.TempDir(), "plugins"))
	src := pluginDir(t, "acme", "1.0.0")
	if err := os.Symlink("lib/data.txt", filepath.Join(src, "inside")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	p, err := install(t, s, src, false)
	if err != nil {
		t.Fatalf("relative inside link refused: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(p.Dir, "inside")); string(data) != "1.0.0" {
		t.Fatalf("link not kept: %q", data)
	}

	bad := pluginDir(t, "acme", "2.0.0")
	if err := os.Symlink("../../etc/passwd", filepath.Join(bad, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := install(t, s, bad, false); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping link: %v", err)
	}
	list, _ := s.List()
	if len(list) != 1 || len(list[0].Versions) != 1 {
		t.Fatalf("a failed install left a version behind: %+v", list)
	}
}

func TestInstallRefusesThePluginsDirInsideTheSource(t *testing.T) {
	src := pluginDir(t, "acme", "1.0.0")
	s := fsstore.New(filepath.Join(src, "plugins"))
	if _, err := install(t, s, src, false); err == nil || !strings.Contains(err.Error(), "contains") {
		t.Fatalf("err = %v", err)
	}
}

func TestActiveReportsBrokenPluginsAndKeepsTheRest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	s := fsstore.New(root)
	if _, err := install(t, s, pluginDir(t, "good", "1.0.0"), false); err != nil {
		t.Fatal(err)
	}
	p, err := install(t, s, pluginDir(t, "bad", "1.0.0"), false)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(p.Dir, pluginproto.ManifestFile), "name: bad\n", 0o644)

	active, failed, err := s.Active()
	if err != nil || len(failed) != 1 || failed[0].Name != "bad" || failed[0].Version != "1.0.0" {
		t.Fatalf("failed = %+v, err = %v", failed, err)
	}
	if len(active) != 1 || active[0].Manifest.Name != "good" {
		t.Fatalf("active = %+v", active)
	}
}

func TestInvalidNamesAreRefused(t *testing.T) {
	s := fsstore.New(t.TempDir())
	for _, tc := range []struct{ name, version string }{{"../x", "1.0.0"}, {"acme", "../1"}} {
		if err := s.Use(tc.name, tc.version); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("Use(%q, %q) = %v", tc.name, tc.version, err)
		}
	}
	if err := s.Remove("../x", ""); err == nil {
		t.Error("Remove accepted a path")
	}
}

func TestInstallRunsTheCheckOnTheCopy(t *testing.T) {
	s := fsstore.New(filepath.Join(t.TempDir(), "plugins"))
	src := pluginDir(t, "acme", "1.0.0")
	m, err := fsstore.ReadManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	var checked string
	refuse := func(dir string) error {
		checked = dir
		if _, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil {
			t.Errorf("the staged copy is incomplete: %v", err)
		}
		return errors.New("plugin does not start")
	}
	if _, err := s.Install(src, m, false, refuse); err == nil || !strings.Contains(err.Error(), "does not start") {
		t.Fatalf("err = %v", err)
	}
	if checked == "" || checked == src {
		t.Fatalf("check ran on %q, want the staged copy", checked)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("a refused plugin was installed: %+v", list)
	}
	if _, err := os.Stat(checked); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the staged copy was left behind: %v", err)
	}
}
