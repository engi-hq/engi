// Package fsstore keeps installed plugins on disk (docs/15 section 15.5):
// each version is a copy of the plugin directory under <dir>/<name>/<version>/,
// and <dir>/<name>/active names the version the server loads.
package fsstore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/pkg/pluginproto"
)

// activeFile names the file holding a plugin's active version.
const activeFile = "active"

// stagingSuffix marks a copy that is still being written.
const stagingSuffix = ".partial"

// Directory and file modes of installed copies.
const (
	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644
)

// Store manages the plugins directory.
type Store struct {
	dir string
}

// New returns a store rooted at dir. The directory is created on first install.
func New(dir string) *Store { return &Store{dir: dir} }

// Installed is one plugin and its versions.
type Installed struct {
	Name     string
	Active   string
	Versions []string
}

// Plugin is a loadable plugin version: its manifest and directory.
type Plugin struct {
	Manifest pluginproto.Manifest
	Dir      string
}

// ReadManifest parses and validates the manifest of the plugin directory src.
func ReadManifest(src string) (pluginproto.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(src, pluginproto.ManifestFile)) // #nosec G304 -- the operator names the directory
	if err != nil {
		return pluginproto.Manifest{}, fmt.Errorf("reading plugin manifest: %w", err)
	}
	return pluginproto.ParseManifest(data)
}

// Install copies the plugin directory src into the store and makes it the
// active version. An installed copy of the same version is an error wrapping
// domain.ErrAlreadyExists unless replace is set. The manifest must already be
// valid (see ReadManifest). check, when not nil, runs against the staged copy
// before it replaces anything, so a plugin that only works from its source
// directory is refused; its error aborts the install.
func (s *Store) Install(src string, m pluginproto.Manifest, replace bool, check func(dir string) error) (Plugin, error) {
	if err := s.checkNames(m.Name, m.Version); err != nil {
		return Plugin{}, err
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolving %s: %w", src, err)
	}
	dst := s.versionDir(m.Name, m.Version)
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolving %s: %w", dst, err)
	}
	if within(absDst, absSrc) {
		return Plugin{}, fmt.Errorf("the plugin directory %s contains the plugins directory", src)
	}
	if _, err := os.Stat(dst); err == nil && !replace {
		return Plugin{}, fmt.Errorf("plugin %s %s is installed (replace it with --force): %w", m.Name, m.Version, domain.ErrAlreadyExists)
	}
	staging := dst + stagingSuffix
	if err := os.RemoveAll(staging); err != nil {
		return Plugin{}, fmt.Errorf("clearing %s: %w", staging, err)
	}
	if err := copyTree(absSrc, staging); err != nil {
		_ = os.RemoveAll(staging)
		return Plugin{}, err
	}
	if check != nil {
		if err := check(staging); err != nil {
			_ = os.RemoveAll(staging)
			return Plugin{}, err
		}
	}
	if err := os.RemoveAll(dst); err != nil {
		_ = os.RemoveAll(staging)
		return Plugin{}, fmt.Errorf("replacing %s: %w", dst, err)
	}
	if err := os.Rename(staging, dst); err != nil {
		return Plugin{}, fmt.Errorf("installing %s: %w", dst, err)
	}
	if err := s.setActive(m.Name, m.Version); err != nil {
		return Plugin{}, err
	}
	return Plugin{Manifest: m, Dir: dst}, nil
}

// List returns every installed plugin, sorted by name, versions sorted.
func (s *Store) List() ([]Installed, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading plugins directory: %w", err)
	}
	var out []Installed
	for _, e := range entries {
		if !e.IsDir() || !pluginproto.ValidName(e.Name()) {
			continue
		}
		in, err := s.installed(e.Name())
		if err != nil {
			return nil, err
		}
		if len(in.Versions) > 0 {
			out = append(out, in)
		}
	}
	return out, nil
}

// Failure is an installed plugin whose active version cannot be loaded.
type Failure struct {
	Name    string
	Version string
	Err     error
}

// Active returns the active version of every installed plugin. A plugin whose
// active version is missing or has an invalid manifest is returned as a
// Failure; the others still load. The error reports an unreadable store.
func (s *Store) Active() ([]Plugin, []Failure, error) {
	all, err := s.List()
	if err != nil {
		return nil, nil, err
	}
	var (
		out    []Plugin
		failed []Failure
	)
	for _, in := range all {
		if in.Active == "" {
			failed = append(failed, Failure{Name: in.Name, Err: errors.New("no active version")})
			continue
		}
		dir := s.versionDir(in.Name, in.Active)
		m, err := ReadManifest(dir)
		if err == nil && (m.Name != in.Name || m.Version != in.Active) {
			err = fmt.Errorf("the manifest says %s %s", m.Name, m.Version)
		}
		if err != nil {
			failed = append(failed, Failure{Name: in.Name, Version: in.Active, Err: err})
			continue
		}
		out = append(out, Plugin{Manifest: m, Dir: dir})
	}
	return out, failed, nil
}

// Use makes an installed version active.
func (s *Store) Use(name, version string) error {
	if err := s.checkNames(name, version); err != nil {
		return err
	}
	if _, err := os.Stat(s.versionDir(name, version)); err != nil {
		return fmt.Errorf("plugin %s %s: %w", name, version, domain.ErrNotFound)
	}
	return s.setActive(name, version)
}

// Remove deletes one version, or the whole plugin when version is empty. The
// active version can only be removed together with the plugin.
func (s *Store) Remove(name, version string) error {
	if !pluginproto.ValidName(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	in, err := s.installed(name)
	if err != nil {
		return err
	}
	if len(in.Versions) == 0 {
		return fmt.Errorf("plugin %s: %w", name, domain.ErrNotFound)
	}
	if version == "" {
		if err := os.RemoveAll(filepath.Join(s.dir, name)); err != nil {
			return fmt.Errorf("removing plugin %s: %w", name, err)
		}
		return nil
	}
	if !slices.Contains(in.Versions, version) {
		return fmt.Errorf("plugin %s %s: %w", name, version, domain.ErrNotFound)
	}
	if version == in.Active {
		return fmt.Errorf("plugin %s %s is active: switch with `engi plugin use` first, or remove the whole plugin", name, version)
	}
	if err := os.RemoveAll(s.versionDir(name, version)); err != nil {
		return fmt.Errorf("removing plugin %s %s: %w", name, version, err)
	}
	return nil
}

func (s *Store) installed(name string) (Installed, error) {
	in := Installed{Name: name}
	entries, err := os.ReadDir(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return in, nil
	}
	if err != nil {
		return in, fmt.Errorf("reading plugin %s: %w", name, err)
	}
	for _, e := range entries {
		if e.IsDir() && pluginproto.ValidVersion(e.Name()) {
			in.Versions = append(in.Versions, e.Name())
		}
	}
	slices.Sort(in.Versions)
	active, err := os.ReadFile(filepath.Join(s.dir, name, activeFile)) // #nosec G304 -- name is validated
	switch {
	case err == nil:
		if v := strings.TrimSpace(string(active)); slices.Contains(in.Versions, v) {
			in.Active = v
		}
	case !errors.Is(err, fs.ErrNotExist):
		return in, fmt.Errorf("reading active version of %s: %w", name, err)
	}
	return in, nil
}

func (s *Store) setActive(name, version string) error {
	path := filepath.Join(s.dir, name, activeFile)
	tmp := path + stagingSuffix
	if err := os.WriteFile(tmp, []byte(version+"\n"), fileMode); err != nil {
		return fmt.Errorf("writing active version: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing active version: %w", err)
	}
	return nil
}

func (s *Store) checkNames(name, version string) error {
	if !pluginproto.ValidName(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	if !pluginproto.ValidVersion(version) {
		return fmt.Errorf("invalid plugin version %q", version)
	}
	return nil
}

func (s *Store) versionDir(name, version string) string {
	return filepath.Join(s.dir, name, version)
}

// within reports whether path is root or below it.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyTree copies src to dst. Relative symbolic links that stay inside src are
// kept as links; any other link is refused, so an install never reaches
// outside the plugin directory.
func copyTree(src, dst string) error {
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", path, err)
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return copyLink(src, path, target)
		case d.IsDir():
			if err := os.MkdirAll(target, dirMode); err != nil {
				return fmt.Errorf("creating %s: %w", target, err)
			}
			return nil
		case d.Type().IsRegular():
			return copyFile(path, target)
		}
		return fmt.Errorf("%s: only regular files, directories and links are allowed in a plugin", path)
	})
	if err != nil {
		return fmt.Errorf("copying plugin: %w", err)
	}
	return nil
}

func copyLink(root, path, target string) error {
	link, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("reading link %s: %w", path, err)
	}
	if filepath.IsAbs(link) || !within(filepath.Join(filepath.Dir(path), link), root) {
		return fmt.Errorf("link %s points outside the plugin directory", path)
	}
	if err := os.Symlink(link, target); err != nil {
		return fmt.Errorf("creating link %s: %w", target, err)
	}
	return nil
}

func copyFile(src, dst string) (err error) {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	in, err := os.Open(src) // #nosec G304 -- walking the operator's plugin directory
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	// Keep the executable bits so a plugin binary still runs.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode|(info.Mode().Perm()&0o111)) // #nosec G302 G304 -- target inside the plugins directory
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("writing %s: %w", dst, cerr)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}
	return nil
}
