package localblock_test

// Crash-recovery matrix for the localblock lifecycle (#471a): exercises the
// startup marker-cleanup path, marker parsing/validation rejection branches,
// and the transition failure paths. These are the paths that run on a degraded
// disk or after a crash — the moment when a bug turns one fault into data loss.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
)

func TestCleanupHotMarkersResolvesHotCleanupNeededBlock(t *testing.T) {
	dir := t.TempDir()
	// Hot block that still carries an eviction marker (a crash left the block
	// present after the marker was written) → StateHotCleanupNeeded.
	writeLifecycleFile(t, block.FilePath(dir, 1), "block")
	writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
	writeEvictionMarker(t, dir)

	if err := localblock.CleanupHotMarkers(dir); err != nil {
		t.Fatalf("CleanupHotMarkers: %v", err)
	}

	// The eviction marker is removed and the block classifies Hot again.
	if _, err := os.Stat(localblock.EvictionMarkerPath(dir, 1)); !os.IsNotExist(err) {
		t.Fatalf("eviction marker still present (stat err=%v), want removed", err)
	}
	lifecycle, err := localblock.Classify(dir, 1)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if lifecycle.State != localblock.StateHot {
		t.Fatalf("State after cleanup = %s, want %s", lifecycle.State, localblock.StateHot)
	}
}

func TestCleanupHotMarkersLeavesEvictedBlockMarker(t *testing.T) {
	dir := t.TempDir()
	// Evicted block: index present, no .blk, eviction marker present →
	// StateEvicted (not cleanup-needed). The marker must survive cleanup.
	writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
	writeEvictionMarker(t, dir)

	if err := localblock.CleanupHotMarkers(dir); err != nil {
		t.Fatalf("CleanupHotMarkers: %v", err)
	}

	if _, err := os.Stat(localblock.EvictionMarkerPath(dir, 1)); err != nil {
		t.Fatalf("evicted block's marker must survive cleanup: %v", err)
	}
}

func TestCleanupHotMarkersIgnoresNonMarkerFilesAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	writeLifecycleFile(t, block.FilePath(dir, 1), "block")
	writeLifecycleFile(t, filepath.Join(dir, "unrelated.txt"), "x")

	if err := localblock.CleanupHotMarkers(dir); err != nil {
		t.Fatalf("CleanupHotMarkers with non-marker files: %v", err)
	}
	if err := localblock.CleanupHotMarkers(filepath.Join(dir, "does-not-exist")); err != nil {
		t.Fatalf("CleanupHotMarkers on missing dir should be nil: %v", err)
	}
}

// A corrupt eviction marker makes Classify fail, and CleanupHotMarkers aborts
// the whole sweep on it. This pins the current (fail-the-sweep) behavior that
// issue #467 tracks changing to skip-and-degrade; if that changes, update this
// expectation deliberately.
func TestCleanupHotMarkersAbortsOnCorruptMarker(t *testing.T) {
	dir := t.TempDir()
	writeLifecycleFile(t, block.FilePath(dir, 1), "block")
	writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
	writeLifecycleFile(t, localblock.EvictionMarkerPath(dir, 1), "{ not valid json")

	if err := localblock.CleanupHotMarkers(dir); err == nil {
		t.Fatal("CleanupHotMarkers with a corrupt marker succeeded, want error (current fail-the-sweep behavior, #467)")
	}
}

func TestParseEvictionMarkerBlockID(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantID uint64
		wantOK bool
	}{
		{name: "valid", input: "000000000000002a.blk.eviction.json", wantID: 42, wantOK: true},
		{name: "restore marker suffix", input: "000000000000002a.blk.restore.json", wantOK: false},
		{name: "plain block", input: "000000000000002a.blk", wantOK: false},
		{name: "non-hex stem", input: "not-hex.blk.eviction.json", wantOK: false},
		{name: "empty", input: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := localblock.ParseEvictionMarkerBlockID(tt.input)
			if ok != tt.wantOK || (ok && id != tt.wantID) {
				t.Fatalf("ParseEvictionMarkerBlockID(%q) = (%d, %t), want (%d, %t)", tt.input, id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestWriteEvictionMarkerRejectsInvalidFields(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name   string
		mutate func(*localblock.EvictionMarker)
	}{
		{name: "missing backend key", mutate: func(m *localblock.EvictionMarker) { m.BackendKey = "" }},
		{name: "negative size", mutate: func(m *localblock.EvictionMarker) { m.SizeBytes = -1 }},
		{name: "missing validation token", mutate: func(m *localblock.EvictionMarker) { m.ValidationToken = "" }},
		{name: "missing evicted_at", mutate: func(m *localblock.EvictionMarker) { m.EvictedAtUs = 0 }},
		{name: "missing trigger", mutate: func(m *localblock.EvictionMarker) { m.Trigger = "" }},
		{name: "missing reason", mutate: func(m *localblock.EvictionMarker) { m.Reason = "" }},
		{name: "zero block id", mutate: func(m *localblock.EvictionMarker) { m.BlockID = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := evictionMarkerForTest(1)
			tt.mutate(&marker)
			if err := localblock.WriteEvictionMarker(dir, marker); err == nil {
				t.Fatal("WriteEvictionMarker accepted an invalid marker, want ErrMarkerInvalid")
			}
		})
	}
}

func TestWriteRestoreMarkerRejectsInvalidFields(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name   string
		mutate func(*localblock.RestoreMarker)
	}{
		{name: "missing restored_at", mutate: func(m *localblock.RestoreMarker) { m.RestoredAtUs = 0 }},
		{name: "missing source", mutate: func(m *localblock.RestoreMarker) { m.Source = "" }},
		{name: "missing reason", mutate: func(m *localblock.RestoreMarker) { m.Reason = "" }},
		{name: "zero block id", mutate: func(m *localblock.RestoreMarker) { m.BlockID = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := restoreMarkerForTest(1)
			tt.mutate(&marker)
			if err := localblock.WriteRestoreMarker(dir, marker); err == nil {
				t.Fatal("WriteRestoreMarker accepted an invalid marker, want ErrMarkerInvalid")
			}
		})
	}
}

func TestReadEvictionMarkerRejectsBlockIDMismatch(t *testing.T) {
	dir := t.TempDir()
	// Write a valid marker for block 1, then read it as block 2: the header
	// block_id must not match the key, and Read must reject it.
	if err := localblock.WriteEvictionMarker(dir, evictionMarkerForTest(1)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	src := localblock.EvictionMarkerPath(dir, 1)
	dst := localblock.EvictionMarkerPath(dir, 2)
	data, err := os.ReadFile(src) //nolint:gosec // test path under t.TempDir.
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil { //nolint:gosec // dst is a marker path under t.TempDir.
		t.Fatalf("write marker copy: %v", err)
	}
	if _, err := localblock.ReadEvictionMarker(dir, 2); err == nil {
		t.Fatal("ReadEvictionMarker accepted a block_id mismatch, want ErrMarkerInvalid")
	}
}

func TestRemoveEvictionMarkerIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeEvictionMarker(t, dir)

	if err := localblock.RemoveEvictionMarker(dir, 1); err != nil {
		t.Fatalf("RemoveEvictionMarker: %v", err)
	}
	if _, err := os.Stat(localblock.EvictionMarkerPath(dir, 1)); !os.IsNotExist(err) {
		t.Fatalf("marker still present after removal: %v", err)
	}
	// Removing an already-absent marker is a no-op.
	if err := localblock.RemoveEvictionMarker(dir, 1); err != nil {
		t.Fatalf("RemoveEvictionMarker (absent) should be nil: %v", err)
	}
}

func TestPublishRestoredBlockFailsWhenTempMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := localblock.PublishRestoredBlock(dir, 1, filepath.Join(dir, "no-such-temp"), restoreMarkerForTest(1))
	if err == nil {
		t.Fatal("PublishRestoredBlock with a missing temp file succeeded, want error")
	}
	// Nothing was published.
	if _, err := os.Stat(block.FilePath(dir, 1)); !os.IsNotExist(err) {
		t.Fatalf("restored block should not exist after a failed publish: %v", err)
	}
}
