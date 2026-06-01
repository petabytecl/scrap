package shard

import (
	"fmt"

	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
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
	lifecycle, err := localblock.Classify(s.blocksDir, blockID)
	if err != nil {
		return fmt.Errorf("%w: classify Block %d for metadata read: %w", storeapi.ErrDataLoss, blockID, err)
	}

	switch lifecycle.State {
	case localblock.StateHot, localblock.StateHotCleanupNeeded:
		return nil
	case localblock.StateEvicted:
		return s.ensureEvictedMetadataAuthority(blockID, lifecycle)
	case localblock.StateMetadataLoss, localblock.StateUnexpectedLoss:
		return fmt.Errorf("%w: Block %d local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	default:
		return fmt.Errorf("%w: Block %d unknown local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	}
}

func (s *Shard) ensureEvictedMetadataAuthority(blockID uint64, lifecycle localblock.Lifecycle) error {
	confirmed, err := s.idx.GetConfirmedUpload(blockID)
	if err != nil {
		return fmt.Errorf("%w: Block %d has no committed ConfirmUpload: %w", storeapi.ErrDataLoss, blockID, err)
	}
	return validateRestoreAuthority(confirmed, lifecycle)
}
