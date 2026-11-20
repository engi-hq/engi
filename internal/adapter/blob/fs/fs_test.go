package fs_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/blob/fs"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/port/porttest"
)

func TestContract(t *testing.T) {
	porttest.RunBlobStore(t, func(t *testing.T) port.BlobStore { return fs.New(filepath.Join(t.TempDir(), "blobs")) })
}

func TestNothingEscapesTheBaseDirectory(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "blobs")
	s := fs.New(base)
	_, err := s.Put(context.Background(), port.BlobRef{Execution: "..", ID: "escape"}, port.BlobInfo{}, bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("traversal accepted")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("files outside the base directory: %v", entries)
	}
}

func TestNoTemporaryFilesRemain(t *testing.T) {
	base := t.TempDir()
	s := fs.New(base)
	ref := port.BlobRef{Execution: "exe_1", ID: "a"}
	if _, err := s.Put(context.Background(), ref, port.BlobInfo{Name: "n"}, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(base, "exe_1"))
	if len(entries) != 2 {
		t.Errorf("entries = %v; want data and metadata only", entries)
	}
}
