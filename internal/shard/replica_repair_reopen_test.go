package shard

import (
	"bytes"
	"log/slog"
	"os"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

// #471: reopenReplicaWritersBestEffort was at 0% coverage — it is the writer
// reinstatement path after a failed repair promotion, where a silent failure
// would leave later appends crashing on closed handles instead of failing
// loudly on nil writers.

func newReplicaReopenTestShard(t *testing.T, blocksDir string) *Shard {
	t.Helper()
	return &Shard{
		shardID:   7,
		blocksDir: blocksDir,
		logger:    slog.New(slog.DiscardHandler),
	}
}

func writeReplicaReopenBlock(t *testing.T, blocksDir string, blockID uint64) replicaRepairPaths {
	t.Helper()
	blkPath := block.FilePath(blocksDir, blockID)
	idxPath := block.IdxFilePath(blocksDir, blockID)
	bw, err := block.NewWriter(blkPath, 7, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := bw.AppendDocument("tx-reopen", "a.txt", "text/plain", bytes.NewReader([]byte("body"))); err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close writer: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close index writer: %v", err)
	}
	return replicaRepairPaths{blkFinal: blkPath, idxFinal: idxPath}
}

func TestReopenReplicaWritersBestEffortRestoresWriters(t *testing.T) {
	blocksDir := t.TempDir()
	s := newReplicaReopenTestShard(t, blocksDir)
	paths := writeReplicaReopenBlock(t, blocksDir, 1)

	s.reopenReplicaWritersBestEffort(paths, 1)

	if s.blockWriter == nil || s.idxWriter == nil {
		t.Fatalf("writers after reopen = (%v, %v), want both restored", s.blockWriter, s.idxWriter)
	}
	if s.blockWriter.BlockID() != 1 {
		t.Fatalf("reopened writer Block = %d, want 1", s.blockWriter.BlockID())
	}
	_ = s.blockWriter.Close()
	_ = s.idxWriter.Close()
}

func TestReopenReplicaWritersBestEffortLeavesNilWritersOnFailure(t *testing.T) {
	blocksDir := t.TempDir()
	s := newReplicaReopenTestShard(t, blocksDir)
	// No Block files exist: reopening must fail and leave both writers nil so
	// later appends fail loudly instead of using stale handles.
	paths := replicaRepairPaths{
		blkFinal: block.FilePath(blocksDir, 9),
		idxFinal: block.IdxFilePath(blocksDir, 9),
	}

	s.reopenReplicaWritersBestEffort(paths, 9)

	if s.blockWriter != nil || s.idxWriter != nil {
		t.Fatalf("writers after failed reopen = (%v, %v), want both nil", s.blockWriter, s.idxWriter)
	}
}

func TestReopenReplicaWritersBestEffortClosesBlockWriterOnIndexFailure(t *testing.T) {
	blocksDir := t.TempDir()
	s := newReplicaReopenTestShard(t, blocksDir)
	paths := writeReplicaReopenBlock(t, blocksDir, 1)
	// Replace the .idx with a directory so OpenIndexWriter fails after the
	// block writer opened.
	if err := replaceWithDir(paths.idxFinal); err != nil {
		t.Fatalf("replace idx with dir: %v", err)
	}

	s.reopenReplicaWritersBestEffort(paths, 1)

	if s.blockWriter != nil || s.idxWriter != nil {
		t.Fatalf("writers after index reopen failure = (%v, %v), want both nil", s.blockWriter, s.idxWriter)
	}
}

func replaceWithDir(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Mkdir(path, 0o750)
}
