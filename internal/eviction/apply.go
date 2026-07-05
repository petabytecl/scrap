package eviction

import (
	"context"
	"time"
)

type (
	ApplyBlockFunc     func(PlanBlock) ApplyBlock
	ApplyBlockObserver func(ApplyBlock)
)

func ApplyBlocks(ctx context.Context, plan Plan, now time.Time, apply ApplyBlockFunc, observe ApplyBlockObserver) (ApplyResult, bool) {
	result := ApplyResult{
		PlanID:         plan.PlanID,
		Status:         ApplyStatusNoEffect,
		StartedAtUs:    now.UnixMicro(),
		MemberHostname: plan.MemberHostname,
		MemberID:       plan.MemberID,
		SelectedBlocks: len(plan.Selected),
	}
	cacheable := true
	for _, selected := range plan.Selected {
		if err := ctx.Err(); err != nil {
			result.Blocks = append(result.Blocks, FailedBlock(selected, err))
			result.FailedBlocks++
			cacheable = false
			break
		}
		blockResult := apply(selected)
		if observe != nil {
			observe(blockResult)
		}
		result.Blocks = append(result.Blocks, blockResult)
		result = countApplyBlock(result, blockResult)
	}
	// Failed blocks are retryable: re-applying the plan attempts them again
	// while already-evicted blocks re-classify as skips, so a result with
	// failures must not be cached as terminal.
	if result.FailedBlocks > 0 {
		cacheable = false
	}
	result.Status = ApplyStatus(result)
	return result, cacheable
}

func ApplySkipCountsByReason(result ApplyResult) map[string]int {
	if result.SkippedBlocks == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, block := range result.Blocks {
		if block.Status == ApplyBlockStatusSkipped && block.Reason != "" {
			counts[block.Reason]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func ApplyStatus(result ApplyResult) string {
	switch {
	case result.FailedBlocks > 0:
		return ApplyStatusFailed
	case result.EvictedBlocks == 0:
		return ApplyStatusNoEffect
	case result.SkippedBlocks > 0:
		return ApplyStatusCompletedWithSkips
	default:
		return ApplyStatusCompleted
	}
}

func SkippedBlock(selected PlanBlock, reason string) ApplyBlock {
	return ApplyBlock{
		BlockID:   selected.BlockID,
		ShardID:   selected.ShardID,
		SizeBytes: selected.SizeBytes,
		Status:    ApplyBlockStatusSkipped,
		Reason:    reason,
	}
}

func FailedBlock(selected PlanBlock, err error) ApplyBlock {
	return ApplyBlock{
		BlockID:   selected.BlockID,
		ShardID:   selected.ShardID,
		SizeBytes: selected.SizeBytes,
		Status:    ApplyBlockStatusFailed,
		Error:     err.Error(),
	}
}

func countApplyBlock(result ApplyResult, block ApplyBlock) ApplyResult {
	switch block.Status {
	case ApplyBlockStatusEvicted:
		result.EvictedBlocks++
		result.BytesFreed += block.BytesFreed
	case ApplyBlockStatusSkipped:
		result.SkippedBlocks++
	case ApplyBlockStatusFailed:
		result.FailedBlocks++
		result.BytesFreed += block.BytesFreed
	}
	return result
}
