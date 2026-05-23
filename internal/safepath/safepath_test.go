package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnderDirAcceptsRootedPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "objects", "..", "object.data")
	got, err := UnderDir(root, path)
	if err != nil {
		t.Fatalf("UnderDir returned error: %v", err)
	}
	want, err := filepath.Abs(filepath.Join(root, "object.data"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if got != want {
		t.Fatalf("UnderDir = %q, want %q", got, want)
	}
}

func TestUnderDirAcceptsRootItself(t *testing.T) {
	root := t.TempDir()
	got, err := UnderDir(root, root)
	if err != nil {
		t.Fatalf("UnderDir returned error: %v", err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	if got != want {
		t.Fatalf("UnderDir = %q, want %q", got, want)
	}
}

func TestUnderDirRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	outside := filepath.Join(base, "outside")
	if _, err := UnderDir(root, outside); err == nil {
		t.Fatal("UnderDir accepted path outside root")
	}
	if _, err := UnderDir(root, filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("UnderDir accepted path traversal outside root")
	}
}
