package shard

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
)

func (s *Shard) ApplyEvictionPlan(ctx context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error) {
	plan, result, ok, err := s.beginEvictionApply(ctx, req.PlanID)
	if err != nil {
		return eviction.ApplyResult{}, err
	}
	if ok {
		return result, nil
	}

	result = s.applyEvictionPlanBlocks(ctx, plan)

	s.mu.Lock()
	delete(s.evictionApplyRunning, plan.PlanID)
	if s.evictionApplyResults == nil {
		s.evictionApplyResults = make(map[string]eviction.ApplyResult)
	}
	s.evictionApplyResults[plan.PlanID] = result
	s.mu.Unlock()

	return result, nil
}

func (s *Shard) beginEvictionApply(ctx context.Context, planID string) (eviction.Plan, eviction.ApplyResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	if planID == "" {
		return eviction.Plan{}, eviction.ApplyResult{}, false, fmt.Errorf("%w: plan_id is required", eviction.ErrInvalidPlanRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if result, ok := s.evictionApplyResults[planID]; ok {
		return eviction.Plan{}, result, true, nil
	}
	if _, ok := s.evictionApplyRunning[planID]; ok {
		return eviction.Plan{}, eviction.ApplyResult{}, false, eviction.ErrApplyInProgress
	}
	if !s.eviction.Enabled {
		return eviction.Plan{}, eviction.ApplyResult{}, false, eviction.ErrApplyDisabled
	}
	plan, err := s.validEvictionPlanForApplyLocked(planID)
	if err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	if s.evictionApplyRunning == nil {
		s.evictionApplyRunning = make(map[string]struct{})
	}
	s.evictionApplyRunning[planID] = struct{}{}
	return plan, eviction.ApplyResult{}, false, nil
}

func (s *Shard) validEvictionPlanForApplyLocked(planID string) (eviction.Plan, error) {
	plan, ok := s.evictionPlans[planID]
	if !ok {
		return eviction.Plan{}, eviction.ErrPlanNotFound
	}
	nowUs := time.Now().UTC().UnixMicro()
	if plan.ExpiresAtUs <= nowUs {
		delete(s.evictionPlans, planID)
		return eviction.Plan{}, eviction.ErrPlanExpired
	}
	if plan.MemberHostname != s.memberHostname || plan.MemberID != s.memberID {
		return eviction.Plan{}, eviction.ErrPlanStale
	}
	return plan, nil
}

func (s *Shard) applyEvictionPlanBlocks(ctx context.Context, plan eviction.Plan) eviction.ApplyResult {
	startedAtUs := time.Now().UTC().UnixMicro()
	result := eviction.ApplyResult{
		PlanID:         plan.PlanID,
		Status:         eviction.ApplyStatusNoEffect,
		StartedAtUs:    startedAtUs,
		MemberHostname: plan.MemberHostname,
		MemberID:       plan.MemberID,
		SelectedBlocks: len(plan.Selected),
	}
	for _, selected := range plan.Selected {
		if err := ctx.Err(); err != nil {
			result.Blocks = append(result.Blocks, failedApplyBlock(selected, err))
			result.FailedBlocks++
			break
		}
		blockResult := s.applyEvictionBlock(plan, selected)
		result.Blocks = append(result.Blocks, blockResult)
		switch blockResult.Status {
		case eviction.ApplyBlockStatusEvicted:
			result.EvictedBlocks++
			result.BytesFreed += blockResult.BytesFreed
		case eviction.ApplyBlockStatusSkipped:
			result.SkippedBlocks++
		case eviction.ApplyBlockStatusFailed:
			result.FailedBlocks++
		}
	}
	result.CompletedAtUs = time.Now().UTC().UnixMicro()
	result.Status = evictionApplyStatus(result)
	return result
}

func (s *Shard) applyEvictionBlock(plan eviction.Plan, selected eviction.PlanBlock) eviction.ApplyBlock {
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, selected.BlockID)
	if err != nil {
		return failedApplyBlock(selected, fmt.Errorf("classify Block: %w", err))
	}
	if reason := s.evictionApplySkipReason(selected, lifecycle); reason != "" {
		return skippedApplyBlock(selected, reason)
	}

	confirmed, err := s.idx.GetConfirmedUpload(selected.BlockID)
	if err != nil {
		return failedApplyBlock(selected, fmt.Errorf("get ConfirmUpload: %w", err))
	}
	if err := validateEvictionApplyAuthority(selected, confirmed); err != nil {
		return failedApplyBlock(selected, err)
	}
	marker := EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          evictionReasonForMarker(plan),
	}
	if err := WriteEvictionMarker(s.blocksDir, marker); err != nil {
		return failedApplyBlock(selected, err)
	}
	if err := os.Remove(block.FilePath(s.blocksDir, selected.BlockID)); err != nil {
		return failedApplyBlock(selected, fmt.Errorf("remove Block: %w", err))
	}
	if err := syncDirectory(s.blocksDir); err != nil {
		return failedApplyBlock(selected, fmt.Errorf("sync blocks directory: %w", err))
	}
	return eviction.ApplyBlock{
		BlockID:    selected.BlockID,
		ShardID:    selected.ShardID,
		SizeBytes:  selected.SizeBytes,
		Status:     eviction.ApplyBlockStatusEvicted,
		BytesFreed: confirmed.BlockObject.SizeBytes,
	}
}

func (s *Shard) evictionApplySkipReason(selected eviction.PlanBlock, lifecycle LocalBlockLifecycle) string {
	if selected.ShardID != s.shardID {
		return eviction.SkipReasonShardFilter
	}
	if lifecycle.State != LocalBlockStateHot {
		return eviction.SkipReasonLocalStateNotHot
	}
	if s.raft != nil && s.raft.IsLeader() {
		return eviction.SkipReasonLeaderHotCopyRequired
	}
	return ""
}

func validateEvictionApplyAuthority(selected eviction.PlanBlock, confirmed index.ConfirmedUpload) error {
	switch {
	case selected.ShardID != confirmed.ShardID:
		return fmt.Errorf("confirmed Shard mismatch for Block %d", selected.BlockID)
	case selected.SizeBytes != confirmed.BlockObject.SizeBytes:
		return fmt.Errorf("confirmed Block size mismatch for Block %d", selected.BlockID)
	case selected.BackendKey != "" && selected.BackendKey != confirmed.BlockObject.Key:
		return fmt.Errorf("confirmed Backend key mismatch for Block %d", selected.BlockID)
	default:
		return nil
	}
}

func evictionReasonForMarker(plan eviction.Plan) string {
	if plan.Reason != "" {
		return plan.Reason
	}
	return EvictionReasonEvidenceRun
}

func evictionApplyStatus(result eviction.ApplyResult) string {
	switch {
	case result.FailedBlocks > 0:
		return eviction.ApplyStatusFailed
	case result.EvictedBlocks == 0:
		return eviction.ApplyStatusNoEffect
	case result.SkippedBlocks > 0:
		return eviction.ApplyStatusCompletedWithSkips
	default:
		return eviction.ApplyStatusCompleted
	}
}

func skippedApplyBlock(selected eviction.PlanBlock, reason string) eviction.ApplyBlock {
	return eviction.ApplyBlock{
		BlockID:   selected.BlockID,
		ShardID:   selected.ShardID,
		SizeBytes: selected.SizeBytes,
		Status:    eviction.ApplyBlockStatusSkipped,
		Reason:    reason,
	}
}

func failedApplyBlock(selected eviction.PlanBlock, err error) eviction.ApplyBlock {
	return eviction.ApplyBlock{
		BlockID:   selected.BlockID,
		ShardID:   selected.ShardID,
		SizeBytes: selected.SizeBytes,
		Status:    eviction.ApplyBlockStatusFailed,
		Error:     err.Error(),
	}
}
