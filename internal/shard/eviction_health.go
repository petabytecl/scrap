package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
)

type evictionHealthBlockContribution struct {
	State       LocalBlockState
	EvictedSize int64
	Quarantined bool
}

func (s *Shard) EvictionHealthSnapshot(ctx context.Context) (eviction.HealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return eviction.HealthSnapshot{}, err
	}

	snapshot := s.evictionLifecycleHealthSnapshot()
	snapshot.RestoreFailuresByReason = s.restoreFailuresByReason()
	for _, count := range snapshot.RestoreFailuresByReason {
		snapshot.RestoreFailedBlocks += count
	}
	snapshot.Pressure = eviction.HealthPressureOK
	if evictionHealthDegraded(snapshot) {
		snapshot.Pressure = eviction.HealthPressureDegraded
	}
	s.evictionMetricRecorder().SetHealth(s.shardID, snapshot)
	return snapshot, nil
}

func (s *Shard) rebuildEvictionHealthSnapshot(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	contributions, err := s.evictionHealthContributions(ctx)
	if err != nil {
		return err
	}
	s.replaceEvictionHealthContributions(contributions)
	return nil
}

func (s *Shard) evictionHealthContributions(ctx context.Context) (map[uint64]evictionHealthBlockContribution, error) {
	contributions, err := quarantinedEvictionHealthContributions(s.blocksDir)
	if err != nil {
		return nil, err
	}
	if err := s.appendConfirmedUploadHealthContributions(ctx, contributions); err != nil {
		return nil, err
	}
	return contributions, nil
}

func quarantinedEvictionHealthContributions(blocksDir string) (map[uint64]evictionHealthBlockContribution, error) {
	quarantined, err := block.ListQuarantined(blocksDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("shard: list quarantined Blocks: %w", err)
	}

	contributions := make(map[uint64]evictionHealthBlockContribution, len(quarantined))
	for _, blockID := range quarantined {
		contributions[blockID] = evictionHealthBlockContribution{Quarantined: true}
	}
	return contributions, nil
}

func (s *Shard) appendConfirmedUploadHealthContributions(
	ctx context.Context,
	contributions map[uint64]evictionHealthBlockContribution,
) error {
	iter, err := s.idx.ConfirmedUploads()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		upload, err := iter.Next()
		if err == nil {
			if _, ok := contributions[upload.BlockID]; ok {
				continue
			}
			lifecycle, err := ClassifyLocalBlock(s.blocksDir, upload.BlockID)
			if err != nil {
				return fmt.Errorf("shard: classify local Block %d for health: %w", upload.BlockID, err)
			}
			contributions[upload.BlockID] = evictionHealthContributionFromLifecycle(lifecycle)
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return err
	}
	return nil
}

func (s *Shard) replaceEvictionHealthContributions(contributions map[uint64]evictionHealthBlockContribution) {
	s.evictionHealthMu.Lock()
	defer s.evictionHealthMu.Unlock()

	s.evictionHealthBlocks = contributions
	s.evictionHealthSnapshot = eviction.HealthSnapshot{}
	for _, contribution := range contributions {
		applyEvictionHealthContribution(&s.evictionHealthSnapshot, contribution, 1)
	}
}

func (s *Shard) recordEvictionHealthBlock(blockID uint64) error {
	contribution, err := s.evictionHealthContributionForBlock(blockID)
	if err != nil {
		return err
	}

	s.evictionHealthMu.Lock()
	defer s.evictionHealthMu.Unlock()

	if s.evictionHealthBlocks == nil {
		s.evictionHealthBlocks = make(map[uint64]evictionHealthBlockContribution)
	}
	old := s.evictionHealthBlocks[blockID]
	applyEvictionHealthContribution(&s.evictionHealthSnapshot, old, -1)
	s.evictionHealthBlocks[blockID] = contribution
	applyEvictionHealthContribution(&s.evictionHealthSnapshot, contribution, 1)
	return nil
}

func (s *Shard) recordEvictionHealthBlockBestEffort(blockID uint64) {
	if err := s.recordEvictionHealthBlock(blockID); err != nil && s.logger != nil {
		s.logger.Warn("record eviction health failed", "block_id", blockID, "error", err)
	}
}

func (s *Shard) evictionHealthContributionForBlock(blockID uint64) (evictionHealthBlockContribution, error) {
	quarantined, err := fileExists(block.FilePath(s.blocksDir, blockID) + block.QuarantineSuffix)
	if err != nil {
		return evictionHealthBlockContribution{}, err
	}
	if quarantined {
		return evictionHealthBlockContribution{Quarantined: true}, nil
	}
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		return evictionHealthBlockContribution{}, fmt.Errorf("shard: classify local Block %d for health: %w", blockID, err)
	}
	return evictionHealthContributionFromLifecycle(lifecycle), nil
}

func (s *Shard) evictionLifecycleHealthSnapshot() eviction.HealthSnapshot {
	s.evictionHealthMu.Lock()
	defer s.evictionHealthMu.Unlock()
	return s.evictionHealthSnapshot
}

func (s *Shard) restoreFailuresByReason() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.restoreFailuresByBlock) == 0 {
		return nil
	}
	out := make(map[string]int, len(s.restoreFailuresByBlock))
	for _, reason := range s.restoreFailuresByBlock {
		out[reason]++
	}
	return out
}

func evictionHealthContributionFromLifecycle(lifecycle LocalBlockLifecycle) evictionHealthBlockContribution {
	contribution := evictionHealthBlockContribution{State: lifecycle.State}
	if lifecycle.State == LocalBlockStateEvicted && lifecycle.EvictionMarker != nil {
		contribution.EvictedSize = lifecycle.EvictionMarker.SizeBytes
	}
	return contribution
}

func applyEvictionHealthContribution(
	snapshot *eviction.HealthSnapshot,
	contribution evictionHealthBlockContribution,
	delta int,
) {
	if contribution.Quarantined {
		snapshot.QuarantinedBlocks += delta
		return
	}
	switch contribution.State {
	case LocalBlockStateHot, "":
	case LocalBlockStateEvicted:
		snapshot.EvictedBlocks += delta
		snapshot.EvictedBytes += int64(delta) * contribution.EvictedSize
	case LocalBlockStateHotCleanupNeeded:
		snapshot.HotCleanupNeededBlocks += delta
	case LocalBlockStateMetadataLoss:
		snapshot.MetadataLossBlocks += delta
	case LocalBlockStateUnexpectedLoss:
		snapshot.UnexpectedLossBlocks += delta
	}
}

func evictionHealthDegraded(snapshot eviction.HealthSnapshot) bool {
	return snapshot.HotCleanupNeededBlocks > 0 ||
		snapshot.MetadataLossBlocks > 0 ||
		snapshot.UnexpectedLossBlocks > 0 ||
		snapshot.QuarantinedBlocks > 0 ||
		snapshot.RestoreFailedBlocks > 0
}
