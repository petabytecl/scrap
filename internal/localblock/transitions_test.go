package localblock_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
)

func TestPrepareEvictionWritesMarkerFromExpectation(t *testing.T) {
	dir := t.TempDir()
	expected := markerExpectationForTest(3)

	err := localblock.PrepareEviction(
		dir,
		expected,
		time.Unix(20, 0).UnixMicro(),
		localblock.EvictionTriggerOperatorRequested,
		localblock.EvictionReasonEvidenceRun,
	)
	if err != nil {
		t.Fatalf("PrepareEviction: %v", err)
	}

	marker, err := localblock.ReadEvictionMarker(dir, 3)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if marker.BackendKey != expected.BackendKey || marker.ValidationToken != expected.ValidationToken {
		t.Fatalf("marker = %+v, want expectation %+v", marker, expected)
	}
}

func TestValidateEvictionMarkerMatches(t *testing.T) {
	expected := markerExpectationForTest(4)
	lifecycle := localblock.Lifecycle{
		BlockID: 4,
		State:   localblock.StateEvicted,
		EvictionMarker: &localblock.EvictionMarker{
			BlockID:         expected.BlockID,
			BackendKey:      expected.BackendKey,
			SizeBytes:       expected.SizeBytes,
			ValidationToken: expected.ValidationToken,
		},
	}

	if err := localblock.ValidateEvictionMarkerMatches(lifecycle, expected); err != nil {
		t.Fatalf("ValidateEvictionMarkerMatches: %v", err)
	}
}

func TestValidateEvictionMarkerMatchesRejectsDrift(t *testing.T) {
	expected := markerExpectationForTest(5)
	lifecycle := localblock.Lifecycle{
		BlockID: 5,
		State:   localblock.StateEvicted,
		EvictionMarker: &localblock.EvictionMarker{
			BlockID:         expected.BlockID,
			BackendKey:      "wrong-key",
			SizeBytes:       expected.SizeBytes,
			ValidationToken: expected.ValidationToken,
		},
	}

	err := localblock.ValidateEvictionMarkerMatches(lifecycle, expected)
	if err == nil {
		t.Fatal("ValidateEvictionMarkerMatches succeeded, want error")
	}
}

func TestUnlinkBlockDataRemovesOnlyBlockFile(t *testing.T) {
	dir := t.TempDir()
	writeLifecycleFile(t, block.FilePath(dir, 6), "block")
	writeLifecycleFile(t, block.IdxFilePath(dir, 6), "index")

	removed, err := localblock.UnlinkBlockData(dir, 6)
	if err != nil {
		t.Fatalf("UnlinkBlockData: %v", err)
	}
	if !removed {
		t.Fatal("UnlinkBlockData removed = false, want true")
	}
	if _, err := os.Stat(block.FilePath(dir, 6)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(dir, 6)); err != nil {
		t.Fatalf("index should remain local: %v", err)
	}
}

func TestPublishRestoredBlockRecordsLifecycleTransition(t *testing.T) {
	dir := t.TempDir()
	writeLifecycleFile(t, block.IdxFilePath(dir, 7), "index")
	if err := localblock.WriteEvictionMarker(dir, evictionMarkerForTest(7)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	tmp := filepath.Join(dir, ".0000000000000007.blk.restore-test")
	writeLifecycleFile(t, tmp, "restored")

	published, err := localblock.PublishRestoredBlock(dir, 7, tmp, localblock.RestoreMarker{
		RestoredAtUs: time.Unix(30, 0).UnixMicro(),
		Source:       localblock.RestoreSourceBackend,
		Reason:       localblock.RestoreReasonRead,
	})
	if err != nil {
		t.Fatalf("PublishRestoredBlock: %v", err)
	}
	if !published {
		t.Fatal("PublishRestoredBlock published = false, want true")
	}
	if _, err := os.Stat(block.FilePath(dir, 7)); err != nil {
		t.Fatalf("restored Block missing: %v", err)
	}
	if _, err := os.Stat(localblock.EvictionMarkerPath(dir, 7)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
	restore, err := localblock.ReadRestoreMarker(dir, 7)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != localblock.RestoreSourceBackend || restore.Reason != localblock.RestoreReasonRead {
		t.Fatalf("restore marker = %+v, want backend/read", restore)
	}
}

func markerExpectationForTest(blockID uint64) localblock.EvictionMarkerExpectation {
	return localblock.EvictionMarkerExpectation{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0000000000000001/0000000000000003.blk",
		SizeBytes:       4096,
		ValidationToken: validationValueForTest(blockID),
	}
}

func TestUnlinkBlockDataMissingBlockIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	removed, err := localblock.UnlinkBlockData(dir, 42)
	if err != nil {
		t.Fatalf("UnlinkBlockData on missing block: %v", err)
	}
	if removed {
		t.Fatal("removed: got true, want false for already-missing block")
	}
}

func writeRestoreMarkerForScanTest(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	err := localblock.WriteRestoreMarker(dir, localblock.RestoreMarker{
		BlockID:      blockID,
		RestoredAtUs: time.Unix(100, 0).UnixMicro(),
		Source:       localblock.RestoreSourceBackend,
		Reason:       localblock.RestoreReasonRead,
	})
	if err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
}

func TestRecordRestoreScanStampsMarkerOnce(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, 3)

	first := time.Unix(200, 0).UnixMicro()
	if err := localblock.RecordRestoreScan(dir, 3, first); err != nil {
		t.Fatalf("RecordRestoreScan: %v", err)
	}
	marker, err := localblock.ReadRestoreMarker(dir, 3)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if marker.ScannedAtUs != first {
		t.Fatalf("scanned_at_us = %d, want %d", marker.ScannedAtUs, first)
	}
	if marker.RestoredAtUs != time.Unix(100, 0).UnixMicro() {
		t.Fatalf("restored_at_us = %d, want preserved original", marker.RestoredAtUs)
	}

	// A second stamp must not overwrite the first scan record.
	if err := localblock.RecordRestoreScan(dir, 3, time.Unix(300, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan second stamp: %v", err)
	}
	marker, err = localblock.ReadRestoreMarker(dir, 3)
	if err != nil {
		t.Fatalf("ReadRestoreMarker after second stamp: %v", err)
	}
	if marker.ScannedAtUs != first {
		t.Fatalf("scanned_at_us after second stamp = %d, want %d", marker.ScannedAtUs, first)
	}
}

func TestRecordRestoreScanMissingMarkerIsNoOp(t *testing.T) {
	dir := t.TempDir()

	if err := localblock.RecordRestoreScan(dir, 9, time.Unix(200, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan on missing marker: %v", err)
	}
	if _, err := os.Stat(localblock.RestoreMarkerPath(dir, 9)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want not exist", err)
	}
}

func TestRecordRestoreScanRejectsNonPositiveTime(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, 3)

	if err := localblock.RecordRestoreScan(dir, 3, 0); !errors.Is(err, localblock.ErrMarkerInvalid) {
		t.Fatalf("RecordRestoreScan(0) error = %v, want ErrMarkerInvalid", err)
	}
}

func TestRemoveRestoreMarkerRemovesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, 3)

	if err := localblock.RemoveRestoreMarker(dir, 3); err != nil {
		t.Fatalf("RemoveRestoreMarker: %v", err)
	}
	if _, err := os.Stat(localblock.RestoreMarkerPath(dir, 3)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want not exist", err)
	}
	if err := localblock.RemoveRestoreMarker(dir, 3); err != nil {
		t.Fatalf("RemoveRestoreMarker on missing marker: %v", err)
	}
}
