package block_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func createSealedBlock(t *testing.T, dir string, _, blockID uint64) {
	t.Helper()
	blkPath := filepath.Join(dir, blockFileName(blockID))
	idxPath := filepath.Join(dir, idxFileName(blockID))

	bw, err := block.NewWriter(blkPath, 1, blockID)
	if err != nil {
		t.Fatalf("NewWriter(%d): %v", blockID, err)
	}
	body := bytes.Repeat([]byte("X"), 128)
	if _, err := bw.AppendDocument("tx-seal", "doc.bin", "application/octet-stream", bytes.NewReader(body)); err != nil {
		t.Fatalf("AppendDocument(%d): %v", blockID, err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block(%d): %v", blockID, err)
	}

	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter(%d): %v", blockID, err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close idx(%d): %v", blockID, err)
	}
}

func blockFileName(id uint64) string {
	return filepath.Base(block.FilePath("", id))
}

func idxFileName(id uint64) string {
	return filepath.Base(block.IdxFilePath("", id))
}

func TestListSealedBlocks_OldestFirst(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 3)
	createSealedBlock(t, dir, 1, 1)
	createSealedBlock(t, dir, 1, 2)

	blocks, err := block.ListSealedBlocks(dir, 99)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if blocks[0].BlockID != 1 || blocks[1].BlockID != 2 || blocks[2].BlockID != 3 {
		t.Fatalf("expected order [1,2,3], got [%d,%d,%d]", blocks[0].BlockID, blocks[1].BlockID, blocks[2].BlockID)
	}
}

func TestListSealedBlocks_ExcludesOpenBlock(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 1)
	createSealedBlock(t, dir, 1, 2)

	blocks, err := block.ListSealedBlocks(dir, 2)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].BlockID != 1 {
		t.Fatalf("expected block 1, got %d", blocks[0].BlockID)
	}
}

func TestListSealedBlocks_ExcludesQuarantined(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 1)
	createSealedBlock(t, dir, 1, 2)

	oldBlk := filepath.Join(dir, blockFileName(2))
	newBlk := oldBlk + ".quarantine"
	if err := os.Rename(oldBlk, newBlk); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	blocks, err := block.ListSealedBlocks(dir, 99)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].BlockID != 1 {
		t.Fatalf("expected block 1, got %d", blocks[0].BlockID)
	}
}

func TestListSealedBlocks_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	blocks, err := block.ListSealedBlocks(dir, 99)
	if err != nil {
		t.Fatalf("ListSealedBlocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestListSealedBlocks_MalformedFilenameFails(t *testing.T) {
	dir := t.TempDir()
	createSealedBlock(t, dir, 1, 1)
	if err := os.WriteFile(filepath.Join(dir, "1.blk"), []byte("junk"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := block.ListSealedBlocks(dir, 99); err == nil {
		t.Fatal("ListSealedBlocks with malformed filename succeeded, want error")
	}
}
