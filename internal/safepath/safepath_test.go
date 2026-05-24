package safepath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestUnderDirAcceptsRootedPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "objects", "..", "object.data")
	got, err := UnderDir(root, path)
	testutil.RequireNoErrorf(t, err, "UnderDir returned error")
	want, err := filepath.Abs(filepath.Join(root, "object.data"))
	testutil.RequireNoErrorf(t, err, "abs path")
	if got != want {
		t.Fatalf("UnderDir = %q, want %q", got, want)
	}
}

func TestUnderDirAcceptsRootItself(t *testing.T) {
	root := t.TempDir()
	got, err := UnderDir(root, root)
	testutil.RequireNoErrorf(t, err, "UnderDir returned error")
	want, err := filepath.Abs(root)
	testutil.RequireNoErrorf(t, err, "abs root")
	if got != want {
		t.Fatalf("UnderDir = %q, want %q", got, want)
	}
}

func TestUnderDirRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	testutil.RequireNoErrorf(t, os.Mkdir(root, 0o700), "mkdir root")
	outside := filepath.Join(base, "outside")
	if _, err := UnderDir(root, outside); err == nil {
		t.Fatal("UnderDir accepted path outside root")
	}
	if _, err := UnderDir(root, filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("UnderDir accepted path traversal outside root")
	}
}
