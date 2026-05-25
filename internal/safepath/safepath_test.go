package safepath

import (
	"os"
	"path/filepath"
	"strings"
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

func TestUnderDirValidatesRequiredInputs(t *testing.T) {
	tests := []struct {
		name string
		root string
		path string
		want string
	}{
		{name: "missing root", root: "", path: "object", want: "root"},
		{name: "missing path", root: "root", path: "", want: "path"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := UnderDir(test.root, test.path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("UnderDir error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestUnderDirAcceptsRelativePaths(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	testutil.RequireNoErrorf(t, os.Mkdir("root", 0o700), "mkdir root")

	got, err := UnderDir("root", filepath.Join("root", "object.data"))
	testutil.RequireNoErrorf(t, err, "UnderDir returned error")
	want := filepath.Join(base, "root", "object.data")
	if got != want {
		t.Fatalf("UnderDir = %q, want %q", got, want)
	}
}

func TestUnderDirAcceptsNestedMissingPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "objects", "pending", "object.data")
	got, err := UnderDir(root, path)
	testutil.RequireNoErrorf(t, err, "UnderDir returned error")
	if got != path {
		t.Fatalf("UnderDir = %q, want %q", got, path)
	}
}

func TestUnderDirRejectsMissingRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "missing-root")
	_, err := UnderDir(root, filepath.Join(root, "object.data"))
	if err == nil || !strings.Contains(err.Error(), "root symlinks") {
		t.Fatalf("UnderDir error = %v, want root symlink resolution error", err)
	}
}

func TestUnderDirRejectsSymlinkEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	testutil.RequireNoErrorf(t, os.Mkdir(root, 0o700), "mkdir root")
	testutil.RequireNoErrorf(t, os.Mkdir(outside, 0o700), "mkdir outside")
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := UnderDir(root, filepath.Join(link, "object.data")); err == nil {
		t.Fatal("UnderDir accepted symlink escape outside root")
	}
}
