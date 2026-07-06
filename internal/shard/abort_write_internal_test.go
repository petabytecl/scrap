package shard

// Regression tests for the two abort-path hazards from the PR #453 review:
// a committed command whose local apply fails must keep its Block bytes
// (peers indexed them; a restart replay can still index them here), and a
// replica left ahead by a leader-aborted write must roll its overhang back
// instead of rejecting replication until restart.

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestResolveCommitApplyFailurePreservesBytesOnLocalApplyError(t *testing.T) {
	s, attempt, startOffset := shardForAbortWriteTest(t)

	s.resolveCommitApplyFailure(attempt, startOffset, errors.New("pebble: injected apply failure"))

	if got := s.blockWriter.Offset(); got <= startOffset {
		t.Fatalf("block offset = %d, want > %d: committed bytes were truncated", got, startOffset)
	}
	assertAbortWritePrepRemoved(t, attempt)
}

func TestResolveCommitApplyFailureReclaimsBytesOnConflictRejection(t *testing.T) {
	s, attempt, startOffset := shardForAbortWriteTest(t)

	s.resolveCommitApplyFailure(attempt, startOffset, duplicateDocumentConflictError())

	if got := s.blockWriter.Offset(); got != startOffset {
		t.Fatalf("block offset = %d, want %d: conflict frames must be reclaimed", got, startOffset)
	}
	assertAbortWritePrepRemoved(t, attempt)
}

func TestRollbackReplicaOverhangTruncatesAndRemovesPreps(t *testing.T) {
	s, attempt, startOffset := shardForAbortWriteTest(t)
	overhangEnd := s.blockWriter.Offset()

	if err := s.rollbackReplicaOverhangLocked(1, startOffset, overhangEnd); err != nil {
		t.Fatalf("rollbackReplicaOverhangLocked: %v", err)
	}

	if got := s.blockWriter.Offset(); got != startOffset {
		t.Fatalf("block offset = %d, want %d after overhang rollback", got, startOffset)
	}
	assertAbortWritePrepRemoved(t, attempt)
}

func TestRollbackReplicaOverhangRefusesIndexedDocuments(t *testing.T) {
	s, _, startOffset := shardForAbortWriteTest(t)
	overhangEnd := s.blockWriter.Offset()
	writeAbortWriteTestIndexEntry(t, s.idxPath(1), startOffset)

	err := s.rollbackReplicaOverhangLocked(1, startOffset, overhangEnd)
	if err == nil {
		t.Fatal("rollbackReplicaOverhangLocked succeeded, want refusal for indexed overhang")
	}
	if got := s.blockWriter.Offset(); got != overhangEnd {
		t.Fatalf("block offset = %d, want %d: indexed bytes must not be truncated", got, overhangEnd)
	}
}

// shardForAbortWriteTest returns a bare Shard whose open Block holds one
// appended-but-unindexed Document starting at the returned offset, with the
// matching prep file on disk.
func shardForAbortWriteTest(t *testing.T) (*Shard, *openlogWriteAttempt, int64) {
	t.Helper()

	blocksDir := t.TempDir()
	bw, err := block.NewWriter(block.FilePath(blocksDir, 1), 7, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = bw.Close() })

	s := &Shard{
		blockWriter: bw,
		blocksDir:   blocksDir,
		openlogDir:  t.TempDir(),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	startOffset := bw.Offset()
	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:        "tx-abort-write",
		docName:     "doc.xml",
		contentType: "text/xml",
		blockID:     1,
		startOffset: startOffset,
	})
	if err != nil {
		t.Fatalf("beginOpenlogWriteAttempt: %v", err)
	}
	if _, err := bw.AppendDocument("tx-abort-write", "doc.xml", "text/xml", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	return s, attempt, startOffset
}

func writeAbortWriteTestIndexEntry(t *testing.T, idxPath string, firstFrameOff int64) {
	t.Helper()

	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	appendErr := iw.Append(block.IndexEntry{
		TransactionID: "tx-abort-write",
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		FirstFrameOff: firstFrameOff,
		FrameCount:    1,
		TotalBytes:    7,
	})
	if closeErr := iw.Close(); appendErr == nil {
		appendErr = closeErr
	}
	if appendErr != nil {
		t.Fatalf("append index entry: %v", appendErr)
	}
}

func assertAbortWritePrepRemoved(t *testing.T, attempt *openlogWriteAttempt) {
	t.Helper()
	if _, err := os.Stat(attempt.prepPath()); !os.IsNotExist(err) {
		t.Fatalf("prep file still present (stat err=%v), want removed", err)
	}
}
