package shard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func TestDeepScrubVerifyBlockRejectsWrongHeaderIdentity(t *testing.T) {
	dir := t.TempDir()
	const wantShard uint64 = 7
	const blockID uint64 = 1
	blkPath := block.FilePath(dir, blockID)
	idxPath := block.IdxFilePath(dir, blockID)

	bw, err := block.NewWriter(blkPath, wantShard+1, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	result, err := bw.AppendDocument("tx-1", "doc.txt", "text/plain", bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-1",
		DocName:       "doc.txt",
		ContentType:   "text/plain",
		CreatedAt:     time.Now(),
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}); err != nil {
		t.Fatalf("Index Append: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Index Close: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, dir, wantShard, nil, nil)
	verify, err := c.VerifyBlock(blkPath, idxPath)
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(verify.CorruptFrames) == 0 {
		t.Fatal("expected header identity corruption, got clean result")
	}
	if verify.CorruptFrames[0].Type != block.CorruptionHeader {
		t.Fatalf("corruption type = %s, want %s", verify.CorruptFrames[0].Type, block.CorruptionHeader)
	}
}

func TestDeepScrubCheckpointPersistsAcrossCoordinator(t *testing.T) {
	dir := t.TempDir()
	c := newScrubCoordinator(&scrubCoordinatorCoreStub{}, dir, 7, nil, nil)
	c.SetDeepScrubCheckpoint(42)
	got, ok := c.GetDeepScrubCheckpoint()
	if !ok || got != 42 {
		t.Fatalf("checkpoint = (%d, %v), want (42, true)", got, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, deepScrubCheckpointFile)); err != nil {
		t.Fatalf("checkpoint file missing: %v", err)
	}
	restarted := newScrubCoordinator(&scrubCoordinatorCoreStub{}, dir, 7, nil, nil)
	got, ok = restarted.GetDeepScrubCheckpoint()
	if !ok || got != 42 {
		t.Fatalf("restarted checkpoint = (%d, %v), want (42, true)", got, ok)
	}
	restarted.ClearDeepScrubCheckpoint()
	if _, ok := restarted.GetDeepScrubCheckpoint(); ok {
		t.Fatal("expected cleared checkpoint")
	}
}
