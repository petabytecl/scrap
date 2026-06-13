package shard_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestHotCleanupNeededMarkerIsRemovedInBackground(t *testing.T) {
	dir := t.TempDir()
	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks: %v", err)
	}
	writeLifecycleFile(t, block.FilePath(blocksDir, 1), "block")
	writeLifecycleFile(t, block.IdxFilePath(blocksDir, 1), "index")
	writeEvictionMarker(t, blocksDir)

	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	s.WaitLifecycleCleanupForTest()
	if _, err := os.Stat(localblock.EvictionMarkerPath(blocksDir, 1)); !os.IsNotExist(err) {
		t.Fatalf("eviction marker after cleanup: %v, want not exist", err)
	}

	lifecycle, err := localblock.Classify(blocksDir, 1)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if lifecycle.State != localblock.StateHot {
		t.Fatalf("State = %s, want %s", lifecycle.State, localblock.StateHot)
	}
}

func writeEvictionMarker(t *testing.T, dir string) {
	t.Helper()
	if err := localblock.WriteEvictionMarker(dir, evictionMarkerForTest(1)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}

func evictionMarkerForTest(blockID uint64) localblock.EvictionMarker {
	return localblock.EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0000000000000001/0000000000000001.blk",
		SizeBytes:       4096,
		ValidationToken: validationValueForTest(blockID),
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         localblock.EvictionTriggerOperatorRequested,
		Reason:          localblock.EvictionReasonEvidenceRun,
	}
}

func validationValueForTest(blockID uint64) string {
	return "validation-" + strconv.FormatUint(blockID, 10)
}

func writeLifecycleFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
