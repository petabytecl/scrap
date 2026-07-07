package shard

// Regression for the post-merge PR #453 review P1: a projection rebuild swaps
// in a fresh Pebble Projection that never carried the durable applied-index
// watermark (ADR 0030), so the next restart failed the self-snapshot restore
// check as a "partial DataDir restore" and locked the Member out after a
// routine rebuild.

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/index"
)

func TestProjectionRebuildCarriesAppliedIndexWatermark(t *testing.T) {
	ctx := context.Background()
	s := openRebuildWatermarkTestShard(t)

	if _, err := s.WriteDocument(ctx, "tx-watermark", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	snapshot, err := s.raftSnapshotData(42)
	if err != nil {
		t.Fatalf("raftSnapshotData: %v", err)
	}
	if got, ok := s.projectionAppliedIndex(); !ok || got != 42 {
		t.Fatalf("projection applied index = %d (ok=%v), want 42", got, ok)
	}

	if _, err := s.TriggerRebuild(ctx); err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	s.WaitRebuild()

	if got, ok := s.projectionAppliedIndex(); !ok || got != 42 {
		t.Fatalf("projection applied index after rebuild = %d (ok=%v), want 42", got, ok)
	}
	// The rebuilt projection must still satisfy the self-snapshot restore
	// check — this is the restart-after-rebuild path that previously failed
	// closed as a partial DataDir restore.
	if err := s.restoreRaftSnapshot(snapshot); err != nil {
		t.Fatalf("restoreRaftSnapshot after rebuild: %v", err)
	}
}

// A raft snapshot created while the rebuild was preparing persists a newer
// watermark into the projection being replaced; the swap must re-persist it
// into the installed projection.
func TestSwapRebuiltProjectionRestoresNewerWatermark(t *testing.T) {
	dataDir := t.TempDir()
	pebbleDir := filepath.Join(dataDir, "pebble")
	tempDir := filepath.Join(dataDir, "pebble.rebuild-1")
	oldDir := filepath.Join(dataDir, "pebble.previous-1")

	current, err := index.Open(pebbleDir)
	if err != nil {
		t.Fatalf("open current projection: %v", err)
	}
	if err := current.PersistAppliedIndex(100); err != nil {
		t.Fatalf("PersistAppliedIndex: %v", err)
	}

	rebuilt, err := index.Open(tempDir)
	if err != nil {
		t.Fatalf("open rebuilt projection: %v", err)
	}
	if err := rebuilt.PersistAppliedIndex(42); err != nil {
		t.Fatalf("PersistAppliedIndex rebuilt: %v", err)
	}
	if err := rebuilt.Close(); err != nil {
		t.Fatalf("close rebuilt projection: %v", err)
	}

	s := &Shard{idx: current}
	t.Cleanup(func() {
		if s.idx != nil {
			_ = s.idx.Close()
		}
	})

	if _, err := s.finalizeAndSwapRebuiltProjection(func() error { return nil }, pebbleDir, tempDir, oldDir); err != nil {
		t.Fatalf("finalizeAndSwapRebuiltProjection: %v", err)
	}
	if got := s.idx.AppliedIndex(); got != 100 {
		t.Fatalf("applied index after swap = %d, want 100 (newer pre-swap watermark restored)", got)
	}
}

func openRebuildWatermarkTestShard(t *testing.T) *Shard {
	t.Helper()

	s, err := Open(Config{
		DataDir:      t.TempDir(),
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}
