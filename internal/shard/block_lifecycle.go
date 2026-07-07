package shard

import (
	"context"

	"github.com/petabytecl/scrap/internal/localblock"
)

const (
	LifecycleMarkerVersion = localblock.MarkerVersion

	EvictionTriggerOperatorRequested = localblock.EvictionTriggerOperatorRequested
	EvictionReasonEvidenceRun        = localblock.EvictionReasonEvidenceRun

	RestoreSourceBackend    = localblock.RestoreSourceBackend
	RestoreReasonRead       = localblock.RestoreReasonRead
	RestoreReasonValidation = localblock.RestoreReasonValidation
	RestoreReasonRepair     = localblock.RestoreReasonRepair
)

var ErrLifecycleMarkerInvalid = localblock.ErrMarkerInvalid

type LocalBlockState = localblock.State

const (
	LocalBlockStateHot              = localblock.StateHot
	LocalBlockStateEvicted          = localblock.StateEvicted
	LocalBlockStateHotCleanupNeeded = localblock.StateHotCleanupNeeded
	LocalBlockStateMetadataLoss     = localblock.StateMetadataLoss
	LocalBlockStateUnexpectedLoss   = localblock.StateUnexpectedLoss
)

type (
	LocalBlockLifecycle  = localblock.Lifecycle
	EvictionMarker       = localblock.EvictionMarker
	RestoreMarker        = localblock.RestoreMarker
	MarkerCleanupFailure = localblock.MarkerCleanupFailure
)

func EvictionMarkerPath(blocksDir string, blockID uint64) string {
	return localblock.EvictionMarkerPath(blocksDir, blockID)
}

func RestoreMarkerPath(blocksDir string, blockID uint64) string {
	return localblock.RestoreMarkerPath(blocksDir, blockID)
}

func WriteEvictionMarker(blocksDir string, marker EvictionMarker) error {
	return localblock.WriteEvictionMarker(blocksDir, marker)
}

func ReadEvictionMarker(blocksDir string, blockID uint64) (EvictionMarker, error) {
	return localblock.ReadEvictionMarker(blocksDir, blockID)
}

func WriteRestoreMarker(blocksDir string, marker RestoreMarker) error {
	return localblock.WriteRestoreMarker(blocksDir, marker)
}

func ReadRestoreMarker(blocksDir string, blockID uint64) (RestoreMarker, error) {
	return localblock.ReadRestoreMarker(blocksDir, blockID)
}

func ClassifyLocalBlock(blocksDir string, blockID uint64) (LocalBlockLifecycle, error) {
	return localblock.Classify(blocksDir, blockID)
}

func CleanupHotLifecycleMarkers(blocksDir string) ([]MarkerCleanupFailure, error) {
	return localblock.CleanupHotMarkers(blocksDir)
}

func (s *Shard) startLifecycleCleanup() {
	done := make(chan struct{})
	s.lifecycleCleanupDone = done
	go func() {
		defer close(done)
		s.lifecycleMutationMu.Lock()
		defer s.lifecycleMutationMu.Unlock()
		failures, err := localblock.CleanupHotMarkers(s.blocksDir)
		if err != nil {
			s.logger.Warn("lifecycle cleanup failed", "error", err)
			return
		}
		if err := s.rebuildEvictionHealthSnapshot(context.Background()); err != nil {
			s.logger.Warn("rebuild eviction health after lifecycle cleanup failed", "error", err)
		}
		// Recorded after the snapshot rebuild so a skipped Block without a
		// confirmed upload (invisible to the rebuild) still degrades health.
		for _, failure := range failures {
			s.logger.Warn("lifecycle cleanup skipped Block with unreadable marker",
				"block_id", failure.BlockID, "error", failure.Err)
			s.recordEvictionHealthBlockBestEffort(failure.BlockID)
		}
	}()
}

func (s *Shard) WaitLifecycleCleanupForTest() {
	s.waitLifecycleCleanup()
}

func (s *Shard) waitLifecycleCleanup() {
	if s.lifecycleCleanupDone == nil {
		return
	}
	<-s.lifecycleCleanupDone
}
