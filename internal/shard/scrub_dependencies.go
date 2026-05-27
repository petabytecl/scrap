package shard

import (
	"context"
	"errors"
	"fmt"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

func (s *Shard) ListSealedBlocks(_ uint64) ([]block.BlockInfo, error) {
	s.mu.Lock()
	openBlockID := uint64(0)
	if s.blockWriter != nil {
		openBlockID = s.blockWriter.BlockID()
	}
	s.mu.Unlock()

	return block.ListSealedBlocks(s.blocksDir, openBlockID)
}

func (s *Shard) VerifyBlock(blkPath, idxPath string) (block.VerifyResult, error) {
	return block.VerifyBlock(blkPath, idxPath)
}

func (s *Shard) Quarantine(blkPath string) error {
	return block.Quarantine(blkPath)
}

func (s *Shard) ListQuarantined() ([]uint64, error) {
	return block.ListQuarantined(s.blocksDir)
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
	_ scrub.BlockLister       = (*Shard)(nil)
	_ scrub.BlockVerifier     = (*Shard)(nil)
	_ scrub.QuarantineManager = (*Shard)(nil)
)
