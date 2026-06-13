package shard

import (
	"errors"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/block"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestApplyCommitDocumentExactDuplicateNoops(t *testing.T) {
	s := shardForReplayConflictApplyTest(t)
	writeReplayConflictApplyIndex(t, s.blocksDir, replayConflictApplyEntry("tx-apply-replay"))
	if err := s.idx.Put("tx-apply-replay", 1, 1, false); err != nil {
		t.Fatalf("Put projection: %v", err)
	}
	sha := replayConflictApplySHA()

	s.applyCommitDocumentCommand(&scrapv1.CommitDocument{
		TransactionId: "tx-apply-replay",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       2,
		TotalBytes:    7,
		Sha256:        sha[:],
		CreatedAtUs:   time.Unix(20, 0).UnixMicro(),
	}, 12)

	docs, err := s.projectionResolver().ListDocuments("tx-apply-replay")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("projected docs = %d, want 1", len(docs))
	}
	if docs[0].BlockID != 1 {
		t.Fatalf("duplicate apply mutated BlockID = %d, want 1", docs[0].BlockID)
	}
}

func TestApplyCommitDocumentConflictingDuplicateNotifiesProposal(t *testing.T) {
	s := shardForReplayConflictApplyTest(t)
	writeReplayConflictApplyIndex(t, s.blocksDir, replayConflictApplyEntry("tx-apply-conflict"))
	if err := s.idx.Put("tx-apply-conflict", 1, 1, false); err != nil {
		t.Fatalf("Put projection: %v", err)
	}
	ch := make(chan error, 1)
	s.proposals["tx-apply-conflict\x00doc.xml"] = ch
	sha := replayConflictApplySHA()

	s.applyCommitDocumentCommand(&scrapv1.CommitDocument{
		TransactionId: "tx-apply-conflict",
		DocumentName:  "doc.xml",
		ContentType:   "application/xml",
		BlockId:       2,
		TotalBytes:    7,
		Sha256:        sha[:],
		CreatedAtUs:   time.Unix(20, 0).UnixMicro(),
	}, 12)

	select {
	case err := <-ch:
		if !errors.Is(err, storeapi.ErrAlreadyExists) {
			t.Fatalf("proposal error = %v, want ErrAlreadyExists", err)
		}
	default:
		t.Fatal("proposal was not notified")
	}
	docs, err := s.projectionResolver().ListDocuments("tx-apply-conflict")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("projected docs = %d, want 1", len(docs))
	}
	if docs[0].ContentType != "text/xml" {
		t.Fatalf("conflict mutated content type = %q, want text/xml", docs[0].ContentType)
	}
}

func shardForReplayConflictApplyTest(t *testing.T) *Shard {
	t.Helper()
	idx := openApplyTestIndex(t)
	return &Shard{
		blocksDir:  t.TempDir(),
		openlogDir: t.TempDir(),
		idx:        idx,
		proposals:  make(map[string]chan error),
		uploads:    newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}
}

func writeReplayConflictApplyIndex(t *testing.T, blocksDir string, entry block.IndexEntry) {
	t.Helper()
	iw, err := block.NewIndexWriter(block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}
	if err := iw.Append(entry); err != nil {
		_ = iw.Close()
		t.Fatalf("Append: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func replayConflictApplyEntry(txID string) block.IndexEntry {
	return block.IndexEntry{
		TransactionID: txID,
		DocName:       "doc.xml",
		ContentType:   "text/xml",
		CreatedAt:     time.Unix(10, 0),
		FirstFrameOff: block.HeaderSize,
		FrameCount:    1,
		TotalBytes:    7,
		SHA256:        replayConflictApplySHA(),
	}
}

func replayConflictApplySHA() [32]byte {
	sum := [32]byte{}
	sum[0] = 7
	return sum
}
