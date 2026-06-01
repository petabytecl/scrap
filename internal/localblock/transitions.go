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
		return false, fmt.Errorf("remove Block: %w", err)
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return true, fmt.Errorf("sync blocks directory: %w", err)
	}
	return true, nil
}

func PublishRestoredBlock(blocksDir string, blockID uint64, tmpPath string, marker RestoreMarker) (bool, error) {
	if err := os.Rename(tmpPath, block.FilePath(blocksDir, blockID)); err != nil {
		return false, fmt.Errorf("publish restored Block %d: %w", blockID, err)
	}
	if err := SyncDirectory(blocksDir); err != nil {
		return true, fmt.Errorf("sync restored Block %d: %w", blockID, err)
	}
	if err := RecordSuccessfulRestore(blocksDir, blockID, marker); err != nil {
		return true, err
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
