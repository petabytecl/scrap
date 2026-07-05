package shard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
	"github.com/petabytecl/scrap/internal/scrub"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (c *scrubCoordinator) ListSealedBlocks(_ uint64) ([]block.Info, error) {
	openBlockID := c.core.currentOpenBlockID()
	blocks, err := block.ListSealedBlocks(c.blocksDir, openBlockID)
	if err != nil {
		return nil, err
	}
	return c.appendEvictedMarkerBlocks(blocks, openBlockID)
}

func (c *scrubCoordinator) VerifyBlock(blkPath, idxPath string) (block.VerifyResult, error) {
	return block.VerifyBlock(blkPath, idxPath)
}

func (c *scrubCoordinator) Quarantine(blkPath string) error {
	if err := block.Quarantine(blkPath); err != nil {
		return err
	}
	blockID, ok := blockIDFromBlockPath(blkPath)
	if !ok {
		return nil
	}
	c.core.recordEvictionHealthBlockBestEffort(blockID)
	return nil
}

func blockIDFromBlockPath(blkPath string) (uint64, bool) {
	name := filepath.Base(blkPath)
	if !strings.HasSuffix(name, ".blk") {
		return 0, false
	}
	id, err := strconv.ParseUint(strings.TrimSuffix(name, ".blk"), 16, 64)
	return id, err == nil
}

func (c *scrubCoordinator) ClassifyScrubBlock(blockID uint64) (scrub.BlockLocalState, error) {
	lifecycle, err := localblock.Classify(c.blocksDir, blockID)
	if err != nil {
		return "", err
	}
	switch lifecycle.State {
	case localblock.StateHot:
		return scrub.BlockLocalStateHot, nil
	case localblock.StateHotCleanupNeeded:
		return scrub.BlockLocalStateHotCleanupNeeded, nil
	case localblock.StateEvicted:
		return scrub.BlockLocalStateEvicted, nil
	case localblock.StateMetadataLoss:
		return scrub.BlockLocalStateMetadataLoss, nil
	case localblock.StateUnexpectedLoss:
		return scrub.BlockLocalStateUnexpectedLoss, nil
	default:
		return "", fmt.Errorf("unknown local Block state %s", lifecycle.State)
	}
}

func (c *scrubCoordinator) appendEvictedMarkerBlocks(blocks []block.Info, openBlockID uint64) ([]block.Info, error) {
	seen := make(map[uint64]struct{}, len(blocks))
	for _, blk := range blocks {
		seen[blk.BlockID] = struct{}{}
	}

	entries, err := os.ReadDir(c.blocksDir)
	if err != nil {
		return nil, fmt.Errorf("shard: read lifecycle markers for scrub: %w", err)
	}
	for _, entry := range entries {
		blockID, ok := localblock.ParseEvictionMarkerBlockID(entry.Name())
		if !ok || blockID == openBlockID {
			continue
		}
		if _, ok := seen[blockID]; ok {
			continue
		}
		blocks = append(blocks, block.Info{
			BlockID: blockID,
			BlkPath: block.FilePath(c.blocksDir, blockID),
			IdxPath: block.IdxFilePath(c.blocksDir, blockID),
		})
		seen[blockID] = struct{}{}
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].BlockID < blocks[j].BlockID
	})
	return blocks, nil
}

func (s *Shard) InjectProjectionKey(_ context.Context, txID string, blockID uint64, docCount uint16, completed bool) error {
	if txID == "" {
		return errors.New("shard: transaction_id is required")
	}
	if blockID == 0 {
		return errors.New("shard: block_id is required")
	}
	if docCount == 0 {
		return errors.New("shard: doc_count is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.idx == nil {
		return fmt.Errorf("%w: projection is nil", storeapi.ErrDataLoss)
	}
	if err := s.idx.Put(txID, blockID, docCount, completed); err != nil {
		return fmt.Errorf("shard: inject projection key: %w", err)
	}
	return nil
}

var (
	_ scrub.BlockLister          = (*scrubCoordinator)(nil)
	_ scrub.BlockVerifier        = (*scrubCoordinator)(nil)
	_ scrub.QuarantineManager    = (*scrubCoordinator)(nil)
	_ scrub.BlockStateClassifier = (*scrubCoordinator)(nil)
)
