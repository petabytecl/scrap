package shard

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

type stubRaftNode struct {
	applied atomic.Uint64
}

func (s *stubRaftNode) Propose(context.Context, []byte) error      { return nil }
func (s *stubRaftNode) ReadIndex(context.Context) (uint64, error)  { return 0, nil }
func (s *stubRaftNode) Step(context.Context, raftpb.Message) error { return nil }
func (s *stubRaftNode) IsLeader() bool                             { return true }
func (s *stubRaftNode) LeaderID() uint64                           { return 1 }
func (s *stubRaftNode) Term() uint64                               { return 1 }
func (s *stubRaftNode) AppliedIndex() uint64                       { return s.applied.Load() }
func (s *stubRaftNode) CommitIndex() uint64                        { return s.applied.Load() }
func (s *stubRaftNode) WithStableLeadership(fn func() error) error { return fn() }
func (s *stubRaftNode) Stop()                                      {}

func TestWaitForReadIndexApplyReturnsWhenApplied(t *testing.T) {
	rn := &stubRaftNode{}
	rn.applied.Store(7)
	s := &Shard{raft: rn}

	if err := s.waitForReadIndexApply(context.Background(), 7); err != nil {
		t.Fatalf("waitForReadIndexApply: %v", err)
	}
}

func TestWaitForReadIndexApplyFailsOnContextCancel(t *testing.T) {
	rn := &stubRaftNode{}
	s := &Shard{raft: rn}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.waitForReadIndexApply(ctx, 5); err == nil {
		t.Fatal("waitForReadIndexApply with unapplied read index succeeded, want context error")
	}
}

func TestScanMaxBlockIDReservesQuarantinedBlocks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"0000000000000003.blk.quarantine", "0000000000000003.idx.quarantine"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("q"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	next, err := scanMaxBlockID(dir)
	if err != nil {
		t.Fatalf("scanMaxBlockID: %v", err)
	}
	if next != 4 {
		t.Fatalf("next block ID: got %d, want 4 (quarantined Block 3 must stay reserved)", next)
	}
}

func TestSplitReplicatedStoredFramesRejectsExcessiveFrameCount(t *testing.T) {
	if _, err := splitReplicatedStoredFrames([]byte("x"), math.MaxUint32); err == nil {
		t.Fatal("splitReplicatedStoredFrames with excessive frame count succeeded, want error")
	}
}

func TestRecoverProjectionSwapDirsRemovesStaleDirs(t *testing.T) {
	dataDir := t.TempDir()
	pebbleDir := filepath.Join(dataDir, "pebble")
	if err := os.MkdirAll(pebbleDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pebbleDir, "CURRENT"), []byte("live"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, stale := range []string{"pebble.rebuild-1", "pebble.previous-2"} {
		if err := os.MkdirAll(filepath.Join(dataDir, stale), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", stale, err)
		}
	}

	if err := recoverProjectionSwapDirs(dataDir, pebbleDir); err != nil {
		t.Fatalf("recoverProjectionSwapDirs: %v", err)
	}
	for _, stale := range []string{"pebble.rebuild-1", "pebble.previous-2"} {
		if _, err := os.Stat(filepath.Join(dataDir, stale)); !os.IsNotExist(err) {
			t.Fatalf("stale dir %s still present", stale)
		}
	}
	if _, err := os.Stat(filepath.Join(pebbleDir, "CURRENT")); err != nil {
		t.Fatalf("live projection touched: %v", err)
	}
}

func TestRecoverProjectionSwapDirsRestoresMissingProjection(t *testing.T) {
	dataDir := t.TempDir()
	pebbleDir := filepath.Join(dataDir, "pebble")
	prevDir := filepath.Join(dataDir, "pebble.previous-9")
	if err := os.MkdirAll(prevDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prevDir, "CURRENT"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := recoverProjectionSwapDirs(dataDir, pebbleDir); err != nil {
		t.Fatalf("recoverProjectionSwapDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pebbleDir, "CURRENT")); err != nil {
		t.Fatalf("projection not restored from previous dir: %v", err)
	}
	if _, err := os.Stat(prevDir); !os.IsNotExist(err) {
		t.Fatal("previous dir still present after restore")
	}
}

func TestSweepOrphanedStagingFilesRemovesCrashLeftovers(t *testing.T) {
	dir := t.TempDir()
	leftovers := []string{
		".0000000000000005.blk.restore-123456",
		".0000000000000005.idx.restore-654321",
		"0000000000000007.blk.replica-repair",
		"0000000000000007.idx.replica-repair.tmp",
	}
	for _, name := range leftovers {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stale"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	keep := "0000000000000007.blk"
	if err := os.WriteFile(filepath.Join(dir, keep), []byte("live"), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", keep, err)
	}

	if err := sweepOrphanedStagingFiles(dir); err != nil {
		t.Fatalf("sweepOrphanedStagingFiles: %v", err)
	}
	for _, name := range leftovers {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("staging leftover %s still present", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
		t.Fatalf("live block file touched by sweep: %v", err)
	}
}
