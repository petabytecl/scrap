package localblock

import (
	"fmt"
	"os"

	"github.com/petabytecl/scrap/internal/block"
)

// EvictionMarkerExpectation is the Shard-authoritative Backend metadata that a
// local eviction marker must match before restore or cleanup proceeds.
type EvictionMarkerExpectation struct {
	BlockID         uint64
	BackendKey      string
	SizeBytes       int64
	ValidationToken string
}

func PrepareEviction(
	blocksDir string,
	expected EvictionMarkerExpectation,
	evictedAtUs int64,
	trigger string,
	reason string,
) error {
	return WriteEvictionMarker(blocksDir, EvictionMarker{
		BlockID:         expected.BlockID,
		BackendKey:      expected.BackendKey,
		SizeBytes:       expected.SizeBytes,
		ValidationToken: expected.ValidationToken,
		EvictedAtUs:     evictedAtUs,
		Trigger:         trigger,
		Reason:          reason,
	})
}

func ValidateEvictionMarkerMatches(lifecycle Lifecycle, expected EvictionMarkerExpectation) error {
	marker := lifecycle.EvictionMarker
	if marker == nil {
		return fmt.Errorf("evicted Block %d missing eviction marker", expected.BlockID)
	}
	switch {
	case marker.BackendKey != expected.BackendKey:
		return fmt.Errorf("eviction marker backend key mismatch for Block %d", expected.BlockID)
	case marker.SizeBytes != expected.SizeBytes:
		return fmt.Errorf("eviction marker size mismatch for Block %d", expected.BlockID)
	case marker.ValidationToken != expected.ValidationToken:
		return fmt.Errorf("eviction marker validation token mismatch for Block %d", expected.BlockID)
	default:
		return nil
	}
}

func RemoveEvictionMarker(blocksDir string, blockID uint64) error {
	if err := os.Remove(EvictionMarkerPath(blocksDir, blockID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove eviction marker: %w", err)
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return fmt.Errorf("sync lifecycle marker removal: %w", err)
	}
	return nil
}

func UnlinkBlockData(blocksDir string, blockID uint64) (bool, error) {
	if err := os.Remove(block.FilePath(blocksDir, blockID)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("remove Block: %w", err)
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return true, fmt.Errorf("sync blocks directory: %w", err)
	}
	return true, nil
}

func PublishRestoredBlock(blocksDir string, blockID uint64, tmpPath string, marker RestoreMarker) (bool, error) {
	// Write the restore marker BEFORE renaming the .blk into place. Classify
	// keeps the Block Evicted while the .blk is absent, so a crash (or marker
	// write failure) here leaves a retryable Evicted Block. The reverse order
	// can strand a Hot Block with no restore marker, which silently disables
	// the restored-block hot-residency guard and rescan eligibility (#467).
	marker.BlockID = blockID
	if err := WriteRestoreMarker(blocksDir, marker); err != nil {
		return false, err
	}
	if err := os.Rename(tmpPath, block.FilePath(blocksDir, blockID)); err != nil {
		return false, fmt.Errorf("publish restored Block %d: %w", blockID, err)
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return true, fmt.Errorf("sync restored Block %d: %w", blockID, err)
	}
	if err := RemoveEvictionMarker(blocksDir, blockID); err != nil {
		return true, fmt.Errorf("remove eviction marker after restore: %w", err)
	}
	return true, nil
}

func RecordSuccessfulRestore(blocksDir string, blockID uint64, marker RestoreMarker) error {
	marker.BlockID = blockID
	if err := WriteRestoreMarker(blocksDir, marker); err != nil {
		return err
	}
	if err := RemoveEvictionMarker(blocksDir, blockID); err != nil {
		return fmt.Errorf("remove eviction marker after restore: %w", err)
	}
	return nil
}

// RecordRestoreScan writes the durable post-restore scan record for blockID
// so the Block is rescanned exactly once per restore instead of once per
// process restart (#454, ADR 0032). The record is a sidecar file, not a
// restore-marker field, so pre-record binaries keep reading the v1 marker
// after a rollback. It carries the marker's RestoredAtUs so a record from an
// earlier restore generation never suppresses the scan of a later one. A
// missing marker is a no-op.
func RecordRestoreScan(blocksDir string, blockID uint64, scannedAtUs int64) error {
	if scannedAtUs <= 0 {
		return fmt.Errorf("%w: restore scan record scanned_at_us is required", ErrMarkerInvalid)
	}
	marker, err := ReadRestoreMarker(blocksDir, blockID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("localblock: record restore scan for Block %d: %w", blockID, err)
	}
	return WriteRestoreScanRecord(blocksDir, RestoreScanRecord{
		BlockID:      blockID,
		RestoredAtUs: marker.RestoredAtUs,
		ScannedAtUs:  scannedAtUs,
	})
}

// RestorePendingScan reports whether blockID carries a restore marker without
// a matching post-restore scan record. An unreadable marker or record keeps
// the Block scan-pending: a rescan is safe, a skipped scan is not.
func RestorePendingScan(blocksDir string, blockID uint64) bool {
	marker, err := ReadRestoreMarker(blocksDir, blockID)
	if err != nil {
		return !os.IsNotExist(err)
	}
	record, err := ReadRestoreScanRecord(blocksDir, blockID)
	if err != nil {
		return true
	}
	return record.RestoredAtUs != marker.RestoredAtUs
}

// RemoveRestoreMarker removes the restore marker and its scan record for
// blockID once its restore lifecycle ends (the Block is evicted again).
// Missing files are a no-op.
func RemoveRestoreMarker(blocksDir string, blockID uint64) error {
	removed := false
	for _, path := range []string{RestoreMarkerPath(blocksDir, blockID), RestoreScanRecordPath(blocksDir, blockID)} {
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("remove restore marker: %w", err)
		}
		removed = true
	}
	if !removed {
		return nil
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return fmt.Errorf("sync lifecycle marker removal: %w", err)
	}
	return nil
}
