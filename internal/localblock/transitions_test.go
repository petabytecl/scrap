package localblock_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

const scanRecordTestBlockID = 3

func writeRestoreMarkerForScanTest(t *testing.T, dir string, restoredAtUs int64) {
	t.Helper()
	err := localblock.WriteRestoreMarker(dir, localblock.RestoreMarker{
		BlockID:      scanRecordTestBlockID,
		RestoredAtUs: restoredAtUs,
		Source:       localblock.RestoreSourceBackend,
		Reason:       localblock.RestoreReasonRead,
	})
	if err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
}

func TestRecordRestoreScanWritesGenerationBoundRecord(t *testing.T) {
	dir := t.TempDir()
	restoredAt := time.Unix(100, 0).UnixMicro()
	writeRestoreMarkerForScanTest(t, dir, restoredAt)

	scannedAt := time.Unix(200, 0).UnixMicro()
	if err := localblock.RecordRestoreScan(dir, 3, scannedAt); err != nil {
		t.Fatalf("RecordRestoreScan: %v", err)
	}
	record, err := localblock.ReadRestoreScanRecord(dir, 3)
	if err != nil {
		t.Fatalf("ReadRestoreScanRecord: %v", err)
	}
	if record.RestoredAtUs != restoredAt || record.ScannedAtUs != scannedAt {
		t.Fatalf("record = %+v, want restored_at %d scanned_at %d", record, restoredAt, scannedAt)
	}
	if localblock.RestorePendingScan(dir, 3) {
		t.Fatal("recorded restore must not be scan-pending")
	}
}

// A rollback to a binary that predates the scan record must keep reading the
// restore marker: the record is a sidecar file and the marker stays exactly
// version-1 shaped (strict unknown-field readers accept it).
func TestRecordRestoreScanKeepsMarkerRollbackCompatible(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, time.Unix(100, 0).UnixMicro())
	if err := localblock.RecordRestoreScan(dir, 3, time.Unix(200, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan: %v", err)
	}

	raw, err := os.ReadFile(localblock.RestoreMarkerPath(dir, 3))
	if err != nil {
		t.Fatalf("read marker file: %v", err)
	}
	if strings.Contains(string(raw), "scanned_at_us") {
		t.Fatal("restore marker gained a field older binaries reject")
	}
	if _, err := localblock.ReadRestoreMarker(dir, 3); err != nil {
		t.Fatalf("ReadRestoreMarker after scan record: %v", err)
	}
}

// Regression for the restore-generation race: a scan record from an earlier
// restore must not suppress the scan of a later restore of the same Block.
func TestRestorePendingScanDetectsFreshRestoreGeneration(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, time.Unix(100, 0).UnixMicro())
	if err := localblock.RecordRestoreScan(dir, 3, time.Unix(200, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan: %v", err)
	}

	// The Block is evicted and restored again: a fresh marker generation.
	writeRestoreMarkerForScanTest(t, dir, time.Unix(300, 0).UnixMicro())
	if !localblock.RestorePendingScan(dir, 3) {
		t.Fatal("fresh restore generation must be scan-pending despite the stale record")
	}
}

func TestRestorePendingScanTreatsUnreadableMarkerAsPending(t *testing.T) {
	dir := t.TempDir()
	if localblock.RestorePendingScan(dir, 1) {
		t.Fatal("missing marker must not be pending")
	}
	if err := os.WriteFile(localblock.RestoreMarkerPath(dir, 1), []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}
	if !localblock.RestorePendingScan(dir, 1) {
		t.Fatal("unreadable marker must stay scan-eligible (rescan is safe, a skipped scan is not)")
	}
}

func TestRecordRestoreScanMissingMarkerIsNoOp(t *testing.T) {
	dir := t.TempDir()

	if err := localblock.RecordRestoreScan(dir, 9, time.Unix(200, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan on missing marker: %v", err)
	}
	if _, err := os.Stat(localblock.RestoreScanRecordPath(dir, 9)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scan record stat error = %v, want not exist", err)
	}
}

func TestRecordRestoreScanRejectsNonPositiveTime(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, time.Unix(100, 0).UnixMicro())

	if err := localblock.RecordRestoreScan(dir, 3, 0); !errors.Is(err, localblock.ErrMarkerInvalid) {
		t.Fatalf("RecordRestoreScan(0) error = %v, want ErrMarkerInvalid", err)
	}
}

func TestRemoveRestoreMarkerRemovesMarkerAndRecordAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeRestoreMarkerForScanTest(t, dir, time.Unix(100, 0).UnixMicro())
	if err := localblock.RecordRestoreScan(dir, 3, time.Unix(200, 0).UnixMicro()); err != nil {
		t.Fatalf("RecordRestoreScan: %v", err)
	}

	if err := localblock.RemoveRestoreMarker(dir, 3); err != nil {
		t.Fatalf("RemoveRestoreMarker: %v", err)
	}
	for _, path := range []string{localblock.RestoreMarkerPath(dir, 3), localblock.RestoreScanRecordPath(dir, 3)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s = %v, want not exist", path, err)
		}
	}
	if err := localblock.RemoveRestoreMarker(dir, 3); err != nil {
		t.Fatalf("RemoveRestoreMarker on missing files: %v", err)
	}
}
