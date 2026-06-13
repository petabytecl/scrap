package shard

import (
	"context"
	"fmt"
	"time"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const evictionApplyValidationTimeout = 30 * time.Second

func (s *Shard) ApplyEvictionPlan(ctx context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error) {
	plan, result, ok, err := s.beginEvictionApply(ctx, req.PlanID)
	if err != nil {
		return eviction.ApplyResult{}, err
	}
	if ok {
		return result, nil
	}

	result, cacheable := s.applyEvictionPlanBlocks(ctx, plan)
	validationCtx, cancelValidation := evictionApplyValidationContext(ctx, plan, time.Now().UTC())
	defer cancelValidation()
	s.validateEvictionApply(validationCtx, plan, &result)
	result.CompletedAtUs = time.Now().UTC().UnixMicro()
	s.evictionMetricRecorder().RecordApply(
		s.shardID,
		evictionReasonForMarker(plan),
		result.Status,
		time.Duration(result.CompletedAtUs-result.StartedAtUs)*time.Microsecond,
		eviction.ApplySkipCountsByReason(result),
	)
	s.finishEvictionApply(plan.PlanID, result, cacheable)

	return result, nil
}

func evictionApplyValidationContext(ctx context.Context, plan eviction.Plan, now time.Time) (context.Context, context.CancelFunc) {
	timeout := evictionApplyValidationTimeout
	if plan.ExpiresAtUs > 0 {
		remaining := time.UnixMicro(plan.ExpiresAtUs).Sub(now)
		if remaining < timeout {
			timeout = remaining
		}
	}

	base := context.WithoutCancel(ctx)
	if timeout <= 0 {
		canceled, cancel := context.WithCancel(base)
		cancel()
		return canceled, func() {}
	}
	return context.WithTimeout(base, timeout)
}

func (s *Shard) finishEvictionApply(planID string, result eviction.ApplyResult, cacheable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictionCampaigns.FinishApply(planID, result, cacheable)
}

func (s *Shard) EvictionPlanStatus(ctx context.Context, planID string) (eviction.PlanStatus, error) {
	if err := ctx.Err(); err != nil {
		return eviction.PlanStatus{}, err
	}
	if planID == "" {
		return eviction.PlanStatus{}, fmt.Errorf("%w: plan_id is required", eviction.ErrInvalidPlanRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.evictionCampaigns.Status(planID, s.evictionMember(), time.Now().UTC())
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

	if err := s.ensureEvictionApplyReadyLocked(); err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	start, err := s.evictionCampaigns.BeginApply(planID, s.evictionMember(), time.Now().UTC())
	if err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	if start.Cached {
		return eviction.Plan{}, start.Result, true, nil
	}
	if s.upload.Backend == nil {
		s.evictionCampaigns.FinishApply(planID, eviction.ApplyResult{}, false)
		return eviction.Plan{}, eviction.ApplyResult{}, false, storeapi.NewUnavailable(
			storeapi.UnavailableReasonBackendRestoreUnavailable,
			"Backend restore is not configured",
		)
	}
	return start.Plan, eviction.ApplyResult{}, false, nil
}

func (s *Shard) ensureEvictionApplyReadyLocked() error {
	if !s.eviction.Enabled {
		return eviction.ErrApplyDisabled
	}
	if s.rebuilder != nil && s.rebuilder.InProgress() {
		return fmt.Errorf("%w: eviction apply unavailable", storeapi.ErrRebuilding)
	}
	return nil
}

func (s *Shard) applyEvictionPlanBlocks(ctx context.Context, plan eviction.Plan) (eviction.ApplyResult, bool) {
	return eviction.ApplyBlocks(
		ctx,
		plan,
		time.Now().UTC(),
		func(selected eviction.PlanBlock) eviction.ApplyBlock {
			return s.applyEvictionBlock(plan, selected)
		},
		s.recordEvictionHealthBlockForApplyResult,
	)
}

func (s *Shard) applyEvictionBlock(plan eviction.Plan, selected eviction.PlanBlock) eviction.ApplyBlock {
	s.lifecycleMutationMu.Lock()
	defer s.lifecycleMutationMu.Unlock()

	if s.raft != nil {
		var result eviction.ApplyBlock
		err := s.raft.WithStableLeadership(func() error {
			s.mu.Lock()
			defer s.mu.Unlock()

			result = s.applyEvictionBlockLocked(plan, selected)
			return nil
		})
		if err != nil {
			return eviction.FailedBlock(selected, err)
		}
		return result
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.applyEvictionBlockLocked(plan, selected)
}

func (s *Shard) recordEvictionHealthBlockForApplyResult(result eviction.ApplyBlock) {
	if result.ShardID != s.shardID {
		return
	}
	s.mu.Lock()
	_, err := s.committedConfirmUploadAuthorityLocked(result.BlockID)
	s.mu.Unlock()
	if err != nil {
		return
	}
	s.recordEvictionHealthBlockBestEffort(result.BlockID)
}

func (s *Shard) applyEvictionBlockLocked(plan eviction.Plan, selected eviction.PlanBlock) eviction.ApplyBlock {
	if selected.ShardID != s.shardID {
		return eviction.SkippedBlock(selected, eviction.SkipReasonShardFilter)
	}
	lifecycle, err := localblock.Classify(s.blocksDir, selected.BlockID)
	if err != nil {
		return eviction.FailedBlock(selected, fmt.Errorf("classify Block: %w", err))
	}
	if reason := s.evictionApplySkipReason(selected, lifecycle, time.Now().UTC().UnixMicro()); reason != "" {
		return eviction.SkippedBlock(selected, reason)
	}

	confirmed, err := s.confirmedEvictionApplyAuthorityLocked(selected)
	if err != nil {
		return eviction.FailedBlock(selected, err)
	}
	if s.leaderHotCopyRequired() {
		return eviction.SkippedBlock(selected, eviction.SkipReasonLeaderHotCopyRequired)
	}
	if err := s.prepareEvictionMarkerForApply(plan, lifecycle, confirmed); err != nil {
		return eviction.FailedBlock(selected, err)
	}
	removed, blockResult, err := s.unlinkEvictedBlockIfFollower(selected, lifecycle)
	if blockResult.Status != "" {
		return blockResult
	}
	if err != nil {
		failed := eviction.FailedBlock(selected, err)
		if removed {
			failed.BytesFreed = confirmed.BlockObject.SizeBytes
		}
		return failed
	}
	return eviction.ApplyBlock{
		BlockID:    selected.BlockID,
		ShardID:    selected.ShardID,
		SizeBytes:  selected.SizeBytes,
		Status:     eviction.ApplyBlockStatusEvicted,
		BytesFreed: confirmed.BlockObject.SizeBytes,
	}
}

func (s *Shard) confirmedEvictionApplyAuthorityLocked(selected eviction.PlanBlock) (index.ConfirmedUpload, error) {
	confirmed, err := s.committedConfirmUploadAuthorityLocked(selected.BlockID)
	if err != nil {
		return index.ConfirmedUpload{}, fmt.Errorf("get ConfirmUpload: %w", err)
	}
	if err := validateEvictionApplyAuthority(selected, confirmed); err != nil {
		return index.ConfirmedUpload{}, err
	}
	return confirmed, nil
}

func (s *Shard) prepareEvictionMarkerForApply(plan eviction.Plan, lifecycle localblock.Lifecycle, confirmed index.ConfirmedUpload) error {
	if lifecycle.State == localblock.StateHotCleanupNeeded {
		return validateRestoreAuthority(confirmed, lifecycle)
	}
	return localblock.PrepareEviction(
		s.blocksDir,
		evictionMarkerExpectation(confirmed),
		time.Now().UTC().UnixMicro(),
		localblock.EvictionTriggerOperatorRequested,
		evictionReasonForMarker(plan),
	)
}

func (s *Shard) skipEvictionAfterPreparedMarker(selected eviction.PlanBlock, lifecycle localblock.Lifecycle) eviction.ApplyBlock {
	if lifecycle.State == localblock.StateHot {
		if err := localblock.RemoveEvictionMarker(s.blocksDir, selected.BlockID); err != nil {
			return eviction.FailedBlock(selected, err)
		}
	}
	return eviction.SkippedBlock(selected, eviction.SkipReasonLeaderHotCopyRequired)
}

func (s *Shard) unlinkEvictedBlockIfFollower(selected eviction.PlanBlock, lifecycle localblock.Lifecycle) (bool, eviction.ApplyBlock, error) {
	if s.leaderHotCopyRequired() {
		return false, s.skipEvictionAfterPreparedMarker(selected, lifecycle), nil
	}
	removed, err := localblock.UnlinkBlockData(s.blocksDir, selected.BlockID)
	return removed, eviction.ApplyBlock{}, err
}

func (s *Shard) evictionApplySkipReason(selected eviction.PlanBlock, lifecycle localblock.Lifecycle, nowUs int64) string {
	if selected.ShardID != s.shardID {
		return eviction.SkipReasonShardFilter
	}
	if lifecycle.State != localblock.StateHot && lifecycle.State != localblock.StateHotCleanupNeeded {
		return eviction.SkipReasonLocalStateNotHot
	}
	if s.leaderHotCopyRequired() {
		return eviction.SkipReasonLeaderHotCopyRequired
	}
	if restoredHotResidencyApplies(s.currentHotResidencyWindowSeconds(), lifecycle, nowUs) {
		return eviction.SkipReasonHotResidencyWindow
	}
	return ""
}

func (s *Shard) currentHotResidencyWindowSeconds() int64 {
	return int64(s.eviction.HotResidencyWindow / time.Second)
}

func (s *Shard) leaderHotCopyRequired() bool {
	return s.raft != nil && s.raft.IsLeader()
}

func restoredHotResidencyApplies(windowSeconds int64, lifecycle localblock.Lifecycle, nowUs int64) bool {
	if lifecycle.RestoreMarker == nil || windowSeconds <= 0 {
		return false
	}
	eligibleAtUs := lifecycle.RestoreMarker.RestoredAtUs + windowSeconds*time.Second.Microseconds()
	return nowUs < eligibleAtUs
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
	return localblock.EvictionReasonEvidenceRun
}
