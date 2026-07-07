package scrub

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #471: the repair-install failure ladder (promoteRepairStaging rollback,
// removeQuarantineFiles, atomicWrite error arms) encodes retry-vs-data-loss
// decisions and was largely untested.

func TestPromoteRepairStagingRollsBackBlockOnMissingIndex(t *testing.T) {
	dir := t.TempDir()
	paths := blockRepairPathsFor(dir, 1)
	if err := atomicWrite(paths.blkStaged, []byte("replacement block")); err != nil {
		t.Fatalf("stage block: %v", err)
	}
	// No staged .idx: the second rename must fail and roll the promoted .blk
	// back out, leaving no data-without-index half-install.
	if err := promoteRepairStaging(paths); err == nil {
		t.Fatal("promoteRepairStaging succeeded without a staged index")
	}
	if _, err := os.Stat(paths.blkFinal); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("promoted block stat = %v, want rolled back", err)
	}
}

func TestRemoveQuarantineFilesSurfacesRemovalFailure(t *testing.T) {
	dir := t.TempDir()
	paths := blockRepairPathsFor(dir, 1)
	// A directory where the quarantined .blk file should be makes os.Remove
	// fail with ENOTEMPTY-class errors.
	if err := os.Mkdir(paths.blkQ, 0o750); err != nil {
		t.Fatalf("mkdir quarantine obstruction: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.blkQ, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write obstruction child: %v", err)
	}

	if err := removeQuarantineFiles(paths); err == nil {
		t.Fatal("removeQuarantineFiles succeeded, want removal failure surfaced")
	}
}

func TestAtomicWriteFailsClosedWhenDirectoryMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "file")
	if err := atomicWrite(missing, []byte("data")); err == nil {
		t.Fatal("atomicWrite into missing directory succeeded")
	}
}

func TestAtomicWriteCleansUpTempOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	// A non-empty directory at the destination makes the final rename fail
	// after the temp file was written and synced.
	if err := os.Mkdir(dest, 0o750); err != nil {
		t.Fatalf("mkdir dest obstruction: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write obstruction child: %v", err)
	}

	if err := atomicWrite(dest, []byte("data")); err == nil {
		t.Fatal("atomicWrite over non-empty directory succeeded")
	}
	if _, err := os.Stat(dest + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file stat = %v, want cleaned up on rename failure", err)
	}
}
