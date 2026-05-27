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

	blkPath := block.BlockFilePath(dir, 5)
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

	if err := block.Quarantine(block.BlockFilePath(dir, 2)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if err := block.Quarantine(block.BlockFilePath(dir, 3)); err != nil {
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

	if err := block.Quarantine(block.BlockFilePath(dir, 2)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	sealed, err := block.ListSealedBlocks(dir, 99)
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
