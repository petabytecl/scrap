package shard

// Regression for #465: re-applying a committed Document whose .idx entry was
// already written but whose projection count did not complete (crash between
// the two) must finish the count without appending a duplicate .idx entry.

import (
	"bytes"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
)

func TestApplyCommitDocumentReappliesWithoutDuplicatingIndexEntry(t *testing.T) { //nolint:cyclop // regression fixture builds a multi-Document open Block then asserts index/projection invariants
	s := shardForReplayConflictApplyTest(t)

	// Open Block 1 with two committed Documents on disk (a.xml, b.xml), and keep
	// the writers open so the vulnerable open-Block append branch is exercised.
	bw, err := block.NewWriter(block.FilePath(s.blocksDir, 1), 7, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(block.IdxFilePath(s.blocksDir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	appendApplyIdempotentDoc(t, bw, iw, "tx-idem", "a.xml", []byte("first document"))
	second := appendApplyIdempotentDoc(t, bw, iw, "tx-idem", "b.xml", []byte("second document"))
	s.blockWriter = bw
	s.idxWriter = iw
	t.Cleanup(func() { _ = bw.Close(); _ = iw.Close() })

	// Projection reflects only the first Document counted: the second's .idx
	// entry exists but its count did not complete before the (simulated) crash.
	if err := s.idx.Put("tx-idem", 1, 1, false); err != nil {
		t.Fatalf("Put projection: %v", err)
	}

	// Replay the committed CommitDocument for b.xml.
	if err := s.applyCommitDocumentCommand(&scrapv1.CommitDocument{
		TransactionId: "tx-idem",
		DocumentName:  "b.xml",
		ContentType:   "application/octet-stream",
		BlockId:       1,
		FirstFrameOff: uint64(second.FirstFrameOffset), //nolint:gosec // FirstFrameOffset is a block file offset >= HeaderSize, never negative.
		FrameCount:    second.FrameCount,
		TotalBytes:    second.Size,
		Sha256:        second.SHA256[:],
		CreatedAtUs:   time.Unix(20, 0).UnixMicro(),
	}, 12); err != nil {
		t.Fatalf("applyCommitDocumentCommand: %v", err)
	}

	// The .idx must still hold exactly one entry for b.xml.
	ir, err := block.OpenIndexReader(block.IdxFilePath(s.blocksDir, 1))
	if err != nil {
		t.Fatalf("OpenIndexReader: %v", err)
	}
	defer func() { _ = ir.Close() }()
	count := 0
	for _, e := range ir.Entries() {
		if e.DocName == "b.xml" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("b.xml .idx entries = %d, want 1 (replay duplicated the entry)", count)
	}

	// And the transaction now lists both Documents exactly once.
	docs, err := s.projectionResolver().ListDocuments("tx-idem")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("projected docs = %d, want 2", len(docs))
	}
}

func appendApplyIdempotentDoc(t *testing.T, bw *block.Writer, iw *block.IndexWriter, txID, docName string, body []byte) block.AppendResult {
	t.Helper()
	result, err := bw.AppendDocument(txID, docName, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("AppendDocument %s: %v", docName, err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: txID,
		DocName:       docName,
		ContentType:   "application/octet-stream",
		CreatedAt:     time.Unix(10, 0),
		FirstFrameOff: result.FirstFrameOffset,
		FrameCount:    result.FrameCount,
		TotalBytes:    result.Size,
		SHA256:        result.SHA256,
	}); err != nil {
		t.Fatalf("Append index %s: %v", docName, err)
	}
	return result
}
