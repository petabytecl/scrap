package shard

// Regression for #461: a projection rebuild must carry the content-quarantine
// records and scanner watermark forward. They live only in the projection (no
// Block-file source), so before the fix a rebuild silently re-served
// malware-quarantined Documents.

import (
	"context"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestProjectionRebuildPreservesContentSafetyState(t *testing.T) {
	ctx := context.Background()
	s := openRebuildWatermarkTestShard(t)

	quarantine := index.ContentQuarantine{
		TransactionID: "tx-quarantined",
		DocumentName:  "malware.bin",
		BlockID:       1,
		DetectedAtUs:  1_700_000_000_000_000,
		ConfirmedAtUs: 1_700_000_500_000_000,
		ScanType:      index.ContentQuarantineScanTypeInitial,
		Reason:        index.ContentQuarantineReasonScannerDetection,
	}
	watermark := index.ScannerWatermark{LastScannedBlockID: 7, LastSignatureVersionScanned: "sig-v3"}

	s.mu.Lock()
	putErr := s.idx.PutContentQuarantine(quarantine)
	wmErr := s.idx.PutScannerWatermark(watermark)
	s.mu.Unlock()
	if putErr != nil {
		t.Fatalf("PutContentQuarantine: %v", putErr)
	}
	if wmErr != nil {
		t.Fatalf("PutScannerWatermark: %v", wmErr)
	}

	if _, err := s.TriggerRebuild(ctx); err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	s.WaitRebuild()

	s.mu.Lock()
	got, getErr := s.idx.GetContentQuarantine("tx-quarantined", "malware.bin")
	gotWatermark, wmGetErr := s.idx.GetScannerWatermark()
	s.mu.Unlock()

	if getErr != nil {
		t.Fatalf("content quarantine lost after rebuild: %v", getErr)
	}
	if got != quarantine {
		t.Fatalf("quarantine after rebuild = %+v, want %+v", got, quarantine)
	}
	if wmGetErr != nil {
		t.Fatalf("scanner watermark lost after rebuild: %v", wmGetErr)
	}
	if gotWatermark != watermark {
		t.Fatalf("scanner watermark after rebuild = %+v, want %+v", gotWatermark, watermark)
	}
}
