package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
)

func (s *Shard) EvictionHealthSnapshot(ctx context.Context) (eviction.HealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return eviction.HealthSnapshot{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, err := s.evictionHealthSnapshotLocked()
	if err != nil {
		return eviction.HealthSnapshot{}, err
	}
	s.evictionMetricRecorder().SetHealth(s.shardID, snapshot)
	return snapshot, nil
}

func (s *Shard) evictionHealthSnapshotLocked() (eviction.HealthSnapshot, error) {
	quarantined, err := quarantinedBlockIDs(s.blocksDir)
	if err != nil {
		return eviction.HealthSnapshot{}, err
	}
	iter, err := s.idx.ConfirmedUploads()
	if err != nil {
		return eviction.HealthSnapshot{}, err
	}

	snapshot := eviction.HealthSnapshot{
		Pressure:                eviction.HealthPressureOK,
		QuarantinedBlocks:       len(quarantined),
		RestoreFailuresByReason: copyRestoreFailures(s.restoreFailuresByReason),
	}
	if err := s.collectEvictionLifecycleHealthLocked(&snapshot, quarantined, iter); err != nil {
		return eviction.HealthSnapshot{}, err
	}
	for _, count := range snapshot.RestoreFailuresByReason {
		snapshot.RestoreFailedBlocks += count
	}
	if evictionHealthDegraded(snapshot) {
		snapshot.Pressure = eviction.HealthPressureDegraded
	}
	return snapshot, nil
}

func (s *Shard) collectEvictionLifecycleHealthLocked(
	snapshot *eviction.HealthSnapshot,
	quarantined map[uint64]struct{},
	iter index.ConfirmedUploadIterator,
) error {
	for {
		upload, err := iter.Next()
		if err == nil {
			if _, ok := quarantined[upload.BlockID]; ok {
				continue
			}
			lifecycle, err := ClassifyLocalBlock(s.blocksDir, upload.BlockID)
			if err != nil {
				return fmt.Errorf("shard: classify local Block %d for health: %w", upload.BlockID, err)
			}
			applyLifecycleHealth(snapshot, lifecycle)
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return err
	}
	return nil
}

func applyLifecycleHealth(snapshot *eviction.HealthSnapshot, lifecycle LocalBlockLifecycle) {
	switch lifecycle.State {
	case LocalBlockStateHot:
	case LocalBlockStateEvicted:
		snapshot.EvictedBlocks++
		if lifecycle.EvictionMarker != nil {
			snapshot.EvictedBytes += lifecycle.EvictionMarker.SizeBytes
		}
	case LocalBlockStateHotCleanupNeeded:
		snapshot.HotCleanupNeededBlocks++
	case LocalBlockStateMetadataLoss:
		snapshot.MetadataLossBlocks++
	case LocalBlockStateUnexpectedLoss:
		snapshot.UnexpectedLossBlocks++
	}
}

func evictionHealthDegraded(snapshot eviction.HealthSnapshot) bool {
	return snapshot.HotCleanupNeededBlocks > 0 ||
		snapshot.MetadataLossBlocks > 0 ||
		snapshot.UnexpectedLossBlocks > 0 ||
		snapshot.QuarantinedBlocks > 0 ||
		snapshot.RestoreFailedBlocks > 0
}

func copyRestoreFailures(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for reason, count := range in {
		out[reason] = count
	}
	return out
}

func quarantinedBlockIDs(blocksDir string) (map[uint64]struct{}, error) {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[uint64]struct{}{}, nil
		}
		return nil, fmt.Errorf("shard: read quarantined Blocks: %w", err)
	}
	blockIDs := map[uint64]struct{}{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".blk"+block.QuarantineSuffix) {
			blockID, ok := parseBlockQuarantineName(entry.Name())
			if ok {
				blockIDs[blockID] = struct{}{}
			}
		}
	}
	return blockIDs, nil
}

func parseBlockQuarantineName(name string) (uint64, bool) {
	trimmed := strings.TrimSuffix(name, ".blk"+block.QuarantineSuffix)
	id, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
