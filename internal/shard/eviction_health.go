package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
)

const evictionHealthCacheTTL = 5 * time.Second

func (s *Shard) EvictionHealthSnapshot(ctx context.Context) (eviction.HealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return eviction.HealthSnapshot{}, err
	}
	if snapshot, ok := s.cachedEvictionHealthSnapshot(time.Now()); ok {
		return snapshot, nil
	}
	return s.refreshEvictionHealthSnapshot(ctx)
}

func (s *Shard) cachedEvictionHealthSnapshot(now time.Time) (eviction.HealthSnapshot, bool) {
	s.evictionHealthMu.Lock()
	defer s.evictionHealthMu.Unlock()

	if !s.evictionHealthCacheValid {
		if !s.evictionHealthRefreshing {
			s.evictionHealthRefreshing = true
		}
		return eviction.HealthSnapshot{}, false
	}
	if now.Sub(s.evictionHealthCacheAt) < evictionHealthCacheTTL {
		return s.evictionHealthCache, true
	}
	if !s.evictionHealthRefreshing {
		s.evictionHealthRefreshing = true
		go func() {
			_, _ = s.refreshEvictionHealthSnapshot(context.Background())
		}()
	}
	return s.evictionHealthCache, true
}

func (s *Shard) refreshEvictionHealthSnapshot(ctx context.Context) (eviction.HealthSnapshot, error) {
	snapshot, err := s.computeEvictionHealthSnapshot(ctx)
	if err == nil {
		s.evictionMetricRecorder().SetHealth(s.shardID, snapshot)
	}

	s.evictionHealthMu.Lock()
	defer s.evictionHealthMu.Unlock()
	s.evictionHealthRefreshing = false
	if err != nil {
		return eviction.HealthSnapshot{}, err
	}
	s.evictionHealthCache = snapshot
	s.evictionHealthCacheAt = time.Now()
	s.evictionHealthCacheValid = true
	return snapshot, nil
}

func (s *Shard) invalidateEvictionHealthCache() {
	s.evictionHealthMu.Lock()
	s.evictionHealthCacheValid = false
	s.evictionHealthMu.Unlock()
}

func (s *Shard) computeEvictionHealthSnapshot(ctx context.Context) (eviction.HealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return eviction.HealthSnapshot{}, err
	}
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
		RestoreFailuresByReason: s.restoreFailuresByReason(),
	}
	if err := s.collectEvictionLifecycleHealth(ctx, &snapshot, quarantined, iter); err != nil {
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

func (s *Shard) collectEvictionLifecycleHealth(
	ctx context.Context,
	snapshot *eviction.HealthSnapshot,
	quarantined map[uint64]struct{},
	iter index.ConfirmedUploadIterator,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
