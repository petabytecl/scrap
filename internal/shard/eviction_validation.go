package shard

import (
	"context"
	"fmt"
	"io"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) validateEvictionApply(ctx context.Context, plan eviction.Plan, result *eviction.ApplyResult) {
	sampleLimit := evictionValidationSampleLimit(plan)
	if sampleLimit == 0 || result.FailedBlocks > 0 || result.EvictedBlocks == 0 {
		return
	}

	for _, applied := range result.Blocks {
		if len(result.Validations) >= sampleLimit {
			break
		}
		if applied.Status != eviction.ApplyBlockStatusEvicted {
			continue
		}

		validation := s.validateEvictedBlock(ctx, applied)
		result.Validations = append(result.Validations, validation)
		switch validation.Status {
		case eviction.ValidationStatusPassed:
			result.ValidatedBlocks++
		case eviction.ValidationStatusFailed:
			result.ValidationFailedBlocks++
		}
	}
	if result.ValidationFailedBlocks > 0 {
		result.Status = eviction.ApplyStatusEvictedWithValidationFailure
	}
}

func evictionValidationSampleLimit(plan eviction.Plan) int {
	if evictionReasonForMarker(plan) != eviction.ReasonEvidenceRun || plan.Config.MaxValidateSamples <= 0 {
		return 0
	}
	return plan.Config.MaxValidateSamples
}

func (s *Shard) validateEvictedBlock(ctx context.Context, applied eviction.ApplyBlock) eviction.ValidationBlock {
	validation := eviction.ValidationBlock{
		BlockID: applied.BlockID,
		ShardID: applied.ShardID,
	}
	if err := s.restoreAndReadFirstDocument(ctx, applied.BlockID); err != nil {
		validation.Status = eviction.ValidationStatusFailed
		validation.Error = err.Error()
		return validation
	}
	validation.Status = eviction.ValidationStatusPassed
	return validation
}

func (s *Shard) restoreAndReadFirstDocument(ctx context.Context, blockID uint64) error {
	if err := s.restoreEvictedBlockForReason(ctx, blockID, RestoreReasonValidation); err != nil {
		return err
	}

	entry, err := firstBlockIndexEntry(s.idxPath(blockID))
	if err != nil {
		return err
	}
	rc, err := block.ReadDocument(s.blockPath(blockID), entry)
	if err != nil {
		return fmt.Errorf("%w: validation read Block %d: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if _, err := io.Copy(io.Discard, rc); err != nil {
		_ = rc.Close()
		return fmt.Errorf("%w: validation drain Block %d: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if err := rc.Close(); err != nil {
		return fmt.Errorf("%w: validation close Block %d: %w", storeapi.ErrDataLoss, blockID, err)
	}
	return nil
}

func firstBlockIndexEntry(path string) (block.IndexEntry, error) {
	reader, err := block.OpenIndexReader(path)
	if err != nil {
		return block.IndexEntry{}, fmt.Errorf("%w: validation open index: %w", storeapi.ErrDataLoss, err)
	}
	defer func() { _ = reader.Close() }()

	entries := reader.Entries()
	if len(entries) == 0 {
		return block.IndexEntry{}, fmt.Errorf("%w: validation index has no Documents", storeapi.ErrDataLoss)
	}
	return entries[0], nil
}
