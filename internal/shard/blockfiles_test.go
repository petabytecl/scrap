package shard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanMaxBlockIDReservesLifecycleFiles(t *testing.T) {
	blocksDir := t.TempDir()
	writeScanFileForTest(t, filepath.Join(blocksDir, "0000000000000002.blk"))
	writeScanFileForTest(t, filepath.Join(blocksDir, "0000000000000005.idx"))
	writeScanFileForTest(t, filepath.Join(blocksDir, "0000000000000007.blk.eviction.json"))

	nextID, err := scanMaxBlockID(blocksDir)
	if err != nil {
		t.Fatalf("scanMaxBlockID: %v", err)
	}
	if nextID != 8 {
		t.Fatalf("nextID = %d, want 8", nextID)
	}
}

func writeScanFileForTest(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
