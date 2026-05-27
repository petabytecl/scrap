package peer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWrite_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.dat")

	if err := atomicWrite(path, []byte("hello")); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // test path is controlled
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content: got %q, want %q", got, "hello")
	}

	tmp := path + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("expected .tmp to be cleaned up")
	}
}

func TestAtomicWrite_CleanupOnRenameFailure(t *testing.T) {
	dir := t.TempDir()

	dest := filepath.Join(dir, "subdir", "test.dat")

	tmp := dest + ".tmp"
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatalf("MkdirAll target as dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	err := atomicWrite(dest, []byte("data"))
	if err == nil {
		t.Fatal("expected rename to fail (target is non-empty dir)")
	}

	if _, statErr := os.Stat(tmp); !os.IsNotExist(statErr) {
		t.Fatal("expected .tmp to be cleaned up after rename failure")
	}
}

func TestAtomicWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.dat")

	if err := atomicWrite(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := atomicWrite(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // test path is controlled
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("content: got %q, want %q", got, "second")
	}
}
