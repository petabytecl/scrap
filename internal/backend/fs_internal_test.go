package backend

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyFSErrorBranches(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: ErrTransient},
		{name: "permission", err: os.ErrPermission, want: ErrAuth},
		{name: "exists", err: os.ErrExist, want: ErrConflict},
		{name: "permanent", err: errors.New("disk failed"), want: ErrPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyFSError("op", tt.err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("classifyFSError(%v) = %v, want %v", tt.err, err, tt.want)
			}
		})
	}
}

func TestFSHelpersClassifyFailures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "object")

	if _, err := writeTempObject(path, root, failingReader{}, 1); !errors.Is(err, ErrPermanent) {
		t.Fatalf("writeTempObject failing reader error = %v, want ErrPermanent", err)
	}
	if err := commitTempObject(filepath.Join(root, "missing-temp"), path); !errors.Is(err, ErrNotFound) {
		t.Fatalf("commitTempObject missing temp error = %v, want ErrNotFound", err)
	}
	if err := syncDirectory(filepath.Join(root, "missing-dir")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("syncDirectory missing dir error = %v, want ErrNotFound", err)
	}
}

func TestMkdirAllSyncedCreatesAndPersistsDeepPrefix(t *testing.T) {
	root := t.TempDir()
	// A brand-new multi-level key prefix, as PutObject would create for the
	// first object under it.
	dir := filepath.Join(root, "cell", "shards", "7")

	if err := mkdirAllSynced(dir, root); err != nil {
		t.Fatalf("mkdirAllSynced: %v", err)
	}

	for _, d := range []string{
		filepath.Join(root, "cell"),
		filepath.Join(root, "cell", "shards"),
		dir,
	} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", d)
		}
	}

	// Idempotent on an existing chain (still fsyncs the ancestors, doesn't error).
	if err := mkdirAllSynced(dir, root); err != nil {
		t.Fatalf("mkdirAllSynced (existing): %v", err)
	}
}

func TestMkdirAllSyncedSyncsExistingChainConcurrentCreator(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cell", "shards", "9")

	// Simulate a concurrent creator that ran MkdirAll but has not fsynced yet:
	// the chain already exists when this call runs. mkdirAllSynced must still
	// fsync the ancestors (up to root) rather than early-returning, so the
	// object it is about to write can be acked durably.
	if err := os.MkdirAll(dir, directoryMode); err != nil {
		t.Fatalf("pre-create chain: %v", err)
	}
	if err := mkdirAllSynced(dir, root); err != nil {
		t.Fatalf("mkdirAllSynced on existing chain: %v", err)
	}
}

func TestClassForS3StatusUnknownServerErrorIsTransient(t *testing.T) {
	// 507 Insufficient Storage is unmapped; an unknown 5xx must be transient so
	// restore retries instead of reporting data loss on an intact Block.
	if got := classForS3Status(507); !errors.Is(got, ErrTransient) {
		t.Fatalf("classForS3Status(507) = %v, want ErrTransient", got)
	}
	// An unmapped 4xx stays permanent.
	if got := classForS3Status(418); !errors.Is(got, ErrPermanent) {
		t.Fatalf("classForS3Status(418) = %v, want ErrPermanent", got)
	}
}

func TestListObjectsMissingRootIsEmpty(t *testing.T) {
	objects, err := listObjects(context.Background(), filepath.Join(t.TempDir(), "missing"), "")
	if err != nil {
		t.Fatalf("listObjects missing root: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("listObjects missing root returned %d objects, want 0", len(objects))
	}
}

type failingReader struct{}

func (failingReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
