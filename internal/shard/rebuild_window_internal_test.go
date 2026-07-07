package shard

// Regression for #464: raft applies landing between the rebuild's bulk scan
// and the projection swap used to mutate only the OUTGOING projection, which
// the swap then discarded — committed, ACKed state (Document records AND
// content-quarantine records) became permanently invisible. The rebuild now
// finalizes (delta re-scan + content-safety copy + outbox) and swaps under
// the shard apply lock, so a window apply either lands before the catch-up
// (and is caught up) or after the swap (into the new projection).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestRebuildKeepsStateAppliedDuringRebuildWindow(t *testing.T) {
	ctx := context.Background()
	s := openRebuildWatermarkTestShard(t)

	if _, err := s.WriteDocument(ctx, "tx-before", "doc.bin", "application/octet-stream", "", bytes.NewReader([]byte("pre-rebuild"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	openBlockID := s.currentOpenBlockID()
	content := []byte("window bytes")
	sha := sha256.Sum256(content)
	windowDoc := &scrapv1.CommitDocument{
		TransactionId: "tx-window",
		DocumentName:  "doc.bin",
		ContentType:   "application/octet-stream",
		BlockId:       openBlockID,
		FirstFrameOff: 40, // block.HeaderSize; metadata visibility does not read the bytes
		FrameCount:    1,
		TotalBytes:    int64(len(content)),
		Sha256:        sha[:],
		CreatedAtUs:   time.Now().UTC().UnixMicro(),
	}
	windowQuarantine := &scrapv1.QuarantineDocument{
		TransactionId: "tx-before",
		DocumentName:  "doc.bin",
		BlockId:       openBlockID,
		DetectedAtUs:  time.Now().UTC().UnixMicro(),
		ScanType:      scrapv1.QuarantineScanType_QUARANTINE_SCAN_TYPE_INITIAL,
		Reason:        scrapv1.QuarantineReason_QUARANTINE_REASON_SCANNER_DETECTION,
	}

	// The hook runs in the rebuild-vs-apply window: after the unlocked bulk
	// scan, before the locked finalize+swap — exactly where the raft apply
	// loop can land commits during a real rebuild.
	s.rebuilder.betweenPhasesForTest = func() {
		if err := s.applyCommitDocument(windowDoc, s.raft.AppliedIndex()+1); err != nil {
			t.Errorf("window applyCommitDocument: %v", err)
		}
		if err := s.applyQuarantineDocumentCommand(windowQuarantine); err != nil {
			t.Errorf("window applyQuarantineDocumentCommand: %v", err)
		}
	}

	if _, err := s.TriggerRebuild(ctx); err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	s.WaitRebuild()

	if _, err := s.HeadDocument(ctx, "tx-window", "doc.bin"); err != nil {
		t.Fatalf("HeadDocument(tx-window) after rebuild = %v; commit applied during the rebuild window was lost", err)
	}
	s.mu.Lock()
	_, quarantineErr := s.idx.GetContentQuarantine("tx-before", "doc.bin")
	s.mu.Unlock()
	if quarantineErr != nil {
		t.Fatalf("quarantine applied during the rebuild window lost: %v", quarantineErr)
	}
}
