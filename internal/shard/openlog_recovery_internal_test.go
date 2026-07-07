package shard

// Regression tests for the openlog recovery truncation hazards found in the
// BMAD full-project review: recovery must never truncate a Block region that
// holds committed .idx entries (an ambiguous propose can keep a prep's bytes
// while later Documents commit above it), and truncateFile must never grow a
// Block with zero bytes.

import (
	"bytes"
	"log/slog"
	"os"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestRecoverPrepFileKeepsCommittedBytesAbovePrepOffset(t *testing.T) {
	blocksDir := t.TempDir()
	openlogDir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	s := &Shard{
		blocksDir:  blocksDir,
		openlogDir: openlogDir,
		idx:        idx,
		logger:     slog.New(slog.DiscardHandler),
	}

	// Block 1 holds Document A (uncommitted, has a surviving prep) at the block
	// header offset, then Document B committed above it.
	aOffset, sizeBefore := writeUncommittedThenCommitted(t, blocksDir)

	// A's prep survives; A was never committed (not in the projection).
	writeID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err := s.writePrepFile(writeID, &scrapv1.OpenlogEntry{
		TransactionId: "tx-a",
		DocumentName:  "a.xml",
		BlockId:       1,
		StartOffset:   aOffset,
		ContentType:   "text/xml",
	}); err != nil {
		t.Fatalf("writePrepFile: %v", err)
	}

	candidates, err := s.recoverOpenlog()
	if err != nil {
		t.Fatalf("recoverOpenlog: %v", err)
	}
	// Phase A must not touch the Block; the destructive decision is deferred.
	if info, err := os.Stat(block.FilePath(blocksDir, 1)); err != nil || info.Size() != sizeBefore {
		t.Fatalf("phase A modified the Block (size=%v err=%v), want untouched %d", info, err, sizeBefore)
	}
	if err := s.resolveOpenlogTruncations(candidates); err != nil {
		t.Fatalf("resolveOpenlogTruncations: %v", err)
	}

	info, err := os.Stat(block.FilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("stat block: %v", err)
	}
	if info.Size() != sizeBefore {
		t.Fatalf("block truncated to %d, want %d: committed bytes above prep offset destroyed", info.Size(), sizeBefore)
	}
	// The prep is kept (not removed) so the divergence stays visible.
	if _, err := os.Stat(s.prepPath(writeID)); err != nil {
		t.Fatalf("prep should be retained when truncation is refused: %v", err)
	}
}

// writeUncommittedThenCommitted builds Block 1 with an uncommitted Document A
// at the header offset and a committed Document B (with a .idx entry) above it.
// It returns A's start offset and the block file size after both appends.
func writeUncommittedThenCommitted(t *testing.T, blocksDir string) (uint64, int64) {
	t.Helper()

	bw, err := block.NewWriter(block.FilePath(blocksDir, 1), 7, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	aOffset := bw.Offset()
	if _, err := bw.AppendDocument("tx-a", "a.xml", "text/xml", bytes.NewReader([]byte("uncommitted A"))); err != nil {
		t.Fatalf("append A: %v", err)
	}
	bResult, err := bw.AppendDocument("tx-b", "b.xml", "text/xml", bytes.NewReader([]byte("committed B")))
	if err != nil {
		t.Fatalf("append B: %v", err)
	}
	sizeBefore := bw.Offset()
	if err := bw.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}

	iw, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-b",
		DocName:       "b.xml",
		ContentType:   "text/xml",
		FirstFrameOff: bResult.FirstFrameOffset,
		FrameCount:    bResult.FrameCount,
		TotalBytes:    bResult.Size,
		SHA256:        bResult.SHA256,
	}); err != nil {
		t.Fatalf("append B index: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}
	return uint64(aOffset), sizeBefore //nolint:gosec // aOffset is a block file offset >= HeaderSize, never negative.
}

// A Document made visible by a committed RETRY at a different offset must not
// resolve an earlier rejected duplicate's prep: that prep's unindexed Frames
// are orphans and still need truncation, or doc_seq desynchronizes from the
// .idx position (Codex review on #482).
func TestRecoveryTruncatesRejectedDuplicateWhenRetryCommitted(t *testing.T) {
	blocksDir := t.TempDir()
	openlogDir := t.TempDir()
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	s := &Shard{
		blocksDir:  blocksDir,
		openlogDir: openlogDir,
		idx:        idx,
		logger:     slog.New(slog.DiscardHandler),
	}

	orphanOffset := writeCommittedInstanceThenRejectedDuplicate(t, blocksDir, idx)

	writeID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if err := s.writePrepFile(writeID, &scrapv1.OpenlogEntry{
		TransactionId: "tx-r",
		DocumentName:  "doc.xml",
		BlockId:       1,
		StartOffset:   uint64(orphanOffset), //nolint:gosec // offset >= HeaderSize, never negative
		ContentType:   "text/xml",
	}); err != nil {
		t.Fatalf("writePrepFile: %v", err)
	}

	candidates, err := s.recoverOpenlog()
	if err != nil {
		t.Fatalf("recoverOpenlog: %v", err)
	}
	// The visible Document is the committed instance at a different offset —
	// it must not resolve this prep in phase A.
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want the duplicate's prep deferred", len(candidates))
	}
	if err := s.resolveOpenlogTruncations(candidates); err != nil {
		t.Fatalf("resolveOpenlogTruncations: %v", err)
	}

	info, err := os.Stat(block.FilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("stat block: %v", err)
	}
	if info.Size() != orphanOffset {
		t.Fatalf("block size = %d, want %d: orphan Frames of the rejected duplicate must be truncated", info.Size(), orphanOffset)
	}
	if _, err := os.Stat(s.prepPath(writeID)); !os.IsNotExist(err) {
		t.Fatalf("prep stat = %v, want removed after truncation", err)
	}
}

// writeCommittedInstanceThenRejectedDuplicate builds Block 1 with tx-r/doc.xml
// committed (indexed + in the projection) at the header offset, then the same
// Document's rejected duplicate Frames above it. Returns the orphan offset.
func writeCommittedInstanceThenRejectedDuplicate(t *testing.T, blocksDir string, idx *index.Index) int64 {
	t.Helper()

	bw, err := block.NewWriter(block.FilePath(blocksDir, 1), 7, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	committed, err := bw.AppendDocument("tx-r", "doc.xml", "text/xml", bytes.NewReader([]byte("committed instance")))
	if err != nil {
		t.Fatalf("append committed instance: %v", err)
	}
	orphanOffset := bw.Offset()
	if _, err := bw.AppendDocument("tx-r", "doc.xml", "text/xml", bytes.NewReader([]byte("rejected duplicate"))); err != nil {
		t.Fatalf("append rejected duplicate: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}
	iw, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: "tx-r",
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		FirstFrameOff: committed.FirstFrameOffset,
		FrameCount:    committed.FrameCount,
		TotalBytes:    committed.Size,
		SHA256:        committed.SHA256,
	}); err != nil {
		t.Fatalf("append committed index entry: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}
	if err := addProjectionDocument(idx, "tx-r", 1); err != nil {
		t.Fatalf("addProjectionDocument: %v", err)
	}
	return orphanOffset
}

func TestTruncateFileNeverExtends(t *testing.T) {
	path := t.TempDir() + "/block.blk"
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A target past EOF must not grow the file with zero bytes.
	if err := truncateFile(path, 100); err != nil {
		t.Fatalf("truncateFile grow: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 10 {
		t.Fatalf("file size = %d, want 10: truncateFile must not extend", info.Size())
	}

	// A target below EOF still shrinks.
	if err := truncateFile(path, 4); err != nil {
		t.Fatalf("truncateFile shrink: %v", err)
	}
	if info, err = os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 4 {
		t.Fatalf("file size = %d, want 4 after shrink", info.Size())
	}
}
