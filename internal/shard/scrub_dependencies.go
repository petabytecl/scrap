package shard

import (
	"context"
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

func (c *scrubCoordinator) ListSealedBlocks(_ uint64) ([]block.Info, error) {
	return block.ListSealedBlocks(c.blocksDir, c.core.currentOpenBlockID())
}

func (c *scrubCoordinator) VerifyBlock(blkPath, idxPath string) (block.VerifyResult, error) {
	return block.VerifyBlock(blkPath, idxPath)
}

func (c *scrubCoordinator) Quarantine(blkPath string) error {
	return block.Quarantine(blkPath)
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

	if err := s.idx.Put(txID, blockID, docCount, completed); err != nil {
		return fmt.Errorf("shard: inject projection key: %w", err)
	}
	return nil
}

var (
	_ scrub.BlockLister       = (*scrubCoordinator)(nil)
	_ scrub.BlockVerifier     = (*scrubCoordinator)(nil)
	_ scrub.QuarantineManager = (*scrubCoordinator)(nil)
)
