package shard

import (
	"fmt"

	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) ensureMetadataReadsAllowed(docs []index.ResolvedDocument) error {
	checked := make(map[uint64]struct{}, len(docs))
	for _, doc := range docs {
		if _, ok := checked[doc.BlockID]; ok {
			continue
		}
		checked[doc.BlockID] = struct{}{}
		if err := s.ensureMetadataReadAllowed(doc.BlockID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shard) ensureMetadataReadAllowed(blockID uint64) error {
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		return fmt.Errorf("%w: classify Block %d for metadata read: %w", storeapi.ErrDataLoss, blockID, err)
	}

	switch lifecycle.State {
	case LocalBlockStateHot, LocalBlockStateHotCleanupNeeded, LocalBlockStateEvicted:
		return nil
	case LocalBlockStateMetadataLoss, LocalBlockStateUnexpectedLoss:
		return fmt.Errorf("%w: Block %d local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	default:
		return fmt.Errorf("%w: Block %d unknown local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	}
}
