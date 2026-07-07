package block_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestQuarantine_RenamesBothFiles(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 5)

	blkPath := block.FilePath(dir, 5)
	if err := block.Quarantine(blkPath); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	if _, err := os.Stat(blkPath); !os.IsNotExist(err) {
		t.Fatal("expected .blk to be renamed")
	}
	if _, err := os.Stat(blkPath + ".quarantine"); err != nil {
		t.Fatalf("expected .blk.quarantine to exist: %v", err)
	}

	idxPath := block.IdxFilePath(dir, 5)
	if _, err := os.Stat(idxPath); !os.IsNotExist(err) {
		t.Fatal("expected .idx to be renamed")
	}
	if _, err := os.Stat(idxPath + ".quarantine"); err != nil {
		t.Fatalf("expected .idx.quarantine to exist: %v", err)
	}
}

func TestListQuarantined(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 1)
	createSealedBlock(t, dir, 1, 2)
	createSealedBlock(t, dir, 1, 3)

	if err := block.Quarantine(block.FilePath(dir, 2)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if err := block.Quarantine(block.FilePath(dir, 3)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	q, err := block.ListQuarantined(dir)
	if err != nil {
		t.Fatalf("ListQuarantined: %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("expected 2 quarantined blocks, got %d", len(q))
	}
	if q[0] != 2 || q[1] != 3 {
		t.Fatalf("expected [2,3], got %v", q)
	}
}

func TestQuarantine_ListSealedExcludes(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 1)
	createSealedBlock(t, dir, 1, 2)

	if err := block.Quarantine(block.FilePath(dir, 2)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	sealed, err := block.ListSealedBlocks(dir, 99, nil)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(sealed) != 1 {
		t.Fatalf("expected 1 sealed block, got %d", len(sealed))
	}

	quarantined := filepath.Join(dir, blockFileName(2)+".quarantine")
	if _, err := os.Stat(quarantined); err != nil {
		t.Fatalf("quarantined file missing: %v", err)
	}
}

func TestUnquarantine_RestoresFiles(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 5)

	blkPath := block.FilePath(dir, 5)
	if err := block.Quarantine(blkPath); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	if err := block.Unquarantine(dir, 5); err != nil {
		t.Fatalf("Unquarantine: %v", err)
	}

	if _, err := os.Stat(blkPath); err != nil {
		t.Fatalf("expected .blk restored: %v", err)
	}
	idxPath := block.IdxFilePath(dir, 5)
	if _, err := os.Stat(idxPath); err != nil {
		t.Fatalf("expected .idx restored: %v", err)
	}
	if _, err := os.Stat(blkPath + ".quarantine"); !os.IsNotExist(err) {
		t.Fatal("expected .blk.quarantine removed")
	}
	if _, err := os.Stat(idxPath + ".quarantine"); !os.IsNotExist(err) {
		t.Fatal("expected .idx.quarantine removed")
	}
}

func TestUnquarantine_NotQuarantinedReturnsError(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 5)

	err := block.Unquarantine(dir, 5)
	if err == nil {
		t.Fatal("expected error when block is not quarantined")
	}
}

func TestUnquarantine_RollsBackBlkOnIdxFailure(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 5)

	blkPath := block.FilePath(dir, 5)
	if err := block.Quarantine(blkPath); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	idxDest := block.IdxFilePath(dir, 5)
	if err := os.MkdirAll(idxDest, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(idxDest, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := block.Unquarantine(dir, 5)
	if err == nil {
		t.Fatal("expected error when idx rename fails")
	}

	blkQ := blkPath + ".quarantine"
	if _, statErr := os.Stat(blkQ); statErr != nil {
		t.Fatalf("expected blk to be rolled back to quarantine: %v", statErr)
	}
	if _, statErr := os.Stat(blkPath); !os.IsNotExist(statErr) {
		t.Fatal("expected blk not to exist after rollback")
	}
}

func TestUnquarantine_RefusesToClobberExistingBlock(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 7)

	blkPath := block.FilePath(dir, 7)
	if err := block.Quarantine(blkPath); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	// A replacement Block arrived (e.g. via peer transfer) while the corrupt
	// copy sat in quarantine; Unquarantine must not overwrite it.
	createSealedBlock(t, dir, 1, 7)

	if err := block.Unquarantine(dir, 7); err == nil {
		t.Fatal("Unquarantine over existing block succeeded, want error")
	}

	data, err := os.ReadFile(blkPath) //nolint:gosec // test reads file it just created in a temp dir
	if err != nil {
		t.Fatalf("ReadFile replacement block: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("replacement block is empty after refused unquarantine")
	}
}

// Pins the crash-shape contract behind the #470 rename-order fix: quarantine
// renames .blk before .idx, so a crash between the two leaves
// .blk.quarantine + .idx — a state ListQuarantined still reports for repair.
// The reverse order left .idx.quarantine + .blk, invisible to both
// ListQuarantined and scrub (metadata_loss skip), so the Block was never
// repaired.
func TestListQuarantinedSeesBlkFirstCrashShape(t *testing.T) {
	dir := t.TempDir()
	writeQuarantineCrashFile(t, filepath.Join(dir, "000000000000002a.blk"+block.QuarantineSuffix))
	writeQuarantineCrashFile(t, filepath.Join(dir, "000000000000002a.idx"))

	ids, err := block.ListQuarantined(dir)
	if err != nil {
		t.Fatalf("ListQuarantined: %v", err)
	}
	if len(ids) != 1 || ids[0] != 0x2a {
		t.Fatalf("quarantined IDs = %v, want [42]", ids)
	}
}

func writeQuarantineCrashFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
