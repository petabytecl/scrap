package shard

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func (s *Shard) ApplyEvictionPlan(ctx context.Context, req eviction.ApplyRequest) (eviction.ApplyResult, error) {
	plan, result, ok, err := s.beginEvictionApply(ctx, req.PlanID)
	if err != nil {
		return eviction.ApplyResult{}, err
	}
	if ok {
		return result, nil
	}

	result, cacheable := s.applyEvictionPlanBlocks(ctx, plan)
	s.finishEvictionApply(plan.PlanID, result, cacheable)

	return result, nil
}

func (s *Shard) finishEvictionApply(planID string, result eviction.ApplyResult, cacheable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.evictionApplyRunning, planID)
	if s.evictionApplyResults == nil {
		s.evictionApplyResults = make(map[string]eviction.ApplyResult)
	}
	if cacheable && shouldCacheEvictionApplyResult(result) {
		s.evictionApplyResults[planID] = result
	}
}

func shouldCacheEvictionApplyResult(result eviction.ApplyResult) bool {
	return result.Status != ""
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

	plan, err := s.validEvictionPlanForApplyLocked(planID)
	if err != nil {
		return eviction.PlanStatus{}, err
	}
	if result, ok := s.evictionApplyResults[planID]; ok {
		result := result
		return eviction.PlanStatus{
			PlanID:      planID,
			Status:      result.Status,
			ApplyResult: &result,
		}, nil
	}
	status := eviction.PlanStatusPending
	if _, ok := s.evictionApplyRunning[planID]; ok {
		status = eviction.PlanStatusRunning
	}
	planCopy := plan
	return eviction.PlanStatus{
		PlanID: planID,
		Status: status,
		Plan:   &planCopy,
	}, nil
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
	plan, err := s.validEvictionPlanForApplyLocked(planID)
	if err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	if result, ok := s.evictionApplyResults[planID]; ok {
		return eviction.Plan{}, result, true, nil
	}
	if err := s.markEvictionApplyRunningLocked(planID); err != nil {
		return eviction.Plan{}, eviction.ApplyResult{}, false, err
	}
	return plan, eviction.ApplyResult{}, false, nil
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

func (s *Shard) markEvictionApplyRunningLocked(planID string) error {
	if _, ok := s.evictionApplyRunning[planID]; ok {
		return eviction.ErrApplyInProgress
	}
	if s.upload.Backend == nil {
		return storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "Backend restore is not configured")
	}
	if s.evictionApplyRunning == nil {
		s.evictionApplyRunning = make(map[string]struct{})
	}
	s.evictionApplyRunning[planID] = struct{}{}
	return nil
}

func (s *Shard) validEvictionPlanForApplyLocked(planID string) (eviction.Plan, error) {
	plan, ok := s.evictionPlans[planID]
	if !ok {
		return eviction.Plan{}, eviction.ErrPlanNotFound
	}
	nowUs := time.Now().UTC().UnixMicro()
	if plan.ExpiresAtUs <= nowUs {
		delete(s.evictionPlans, planID)
		delete(s.evictionApplyResults, planID)
		return eviction.Plan{}, eviction.ErrPlanExpired
	}
	if plan.MemberHostname != s.memberHostname || plan.MemberID != s.memberID {
		return eviction.Plan{}, eviction.ErrPlanStale
	}
	return plan, nil
}

func (s *Shard) applyEvictionPlanBlocks(ctx context.Context, plan eviction.Plan) (eviction.ApplyResult, bool) {
	startedAtUs := time.Now().UTC().UnixMicro()
	result := eviction.ApplyResult{
		PlanID:         plan.PlanID,
		Status:         eviction.ApplyStatusNoEffect,
		StartedAtUs:    startedAtUs,
		MemberHostname: plan.MemberHostname,
		MemberID:       plan.MemberID,
		SelectedBlocks: len(plan.Selected),
	}
	cacheable := true
	for _, selected := range plan.Selected {
		if err := ctx.Err(); err != nil {
			result.Blocks = append(result.Blocks, failedApplyBlock(selected, err))
			result.FailedBlocks++
			cacheable = false
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
			result.BytesFreed += blockResult.BytesFreed
		}
		if blockResult.Status == eviction.ApplyBlockStatusFailed {
			break
		}
	}
	result.CompletedAtUs = time.Now().UTC().UnixMicro()
	result.Status = evictionApplyStatus(result)
	return result, cacheable
}

func (s *Shard) applyEvictionBlock(plan eviction.Plan, selected eviction.PlanBlock) eviction.ApplyBlock {
	s.lifecycleMutationMu.Lock()
	defer s.lifecycleMutationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	lifecycle, err := ClassifyLocalBlock(s.blocksDir, selected.BlockID)
	if err != nil {
		return failedApplyBlock(selected, fmt.Errorf("classify Block: %w", err))
	}
	if reason := s.evictionApplySkipReason(selected, lifecycle, time.Now().UTC().UnixMicro()); reason != "" {
		return skippedApplyBlock(selected, reason)
	}

	confirmed, err := s.committedConfirmUploadAuthorityLocked(selected.BlockID)
	if err != nil {
		return failedApplyBlock(selected, fmt.Errorf("get ConfirmUpload: %w", err))
	}
	if err := validateEvictionApplyAuthority(selected, confirmed); err != nil {
		return failedApplyBlock(selected, err)
	}
	if s.leaderHotCopyRequired() {
		return skippedApplyBlock(selected, eviction.SkipReasonLeaderHotCopyRequired)
	}
	if err := s.prepareEvictionMarkerForApply(plan, lifecycle, confirmed); err != nil {
		return failedApplyBlock(selected, err)
	}
	removed, blockResult, err := s.unlinkEvictedBlockIfFollower(selected, lifecycle)
	if blockResult.Status != "" {
		return blockResult
	}
	if err != nil {
		failed := failedApplyBlock(selected, err)
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

func (s *Shard) prepareEvictionMarkerForApply(plan eviction.Plan, lifecycle LocalBlockLifecycle, confirmed index.ConfirmedUpload) error {
	if lifecycle.State == LocalBlockStateHotCleanupNeeded {
		return validateRestoreAuthority(confirmed, lifecycle)
	}
	return WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          evictionReasonForMarker(plan),
	})
}

func (s *Shard) skipEvictionAfterPreparedMarker(selected eviction.PlanBlock, lifecycle LocalBlockLifecycle) eviction.ApplyBlock {
	if lifecycle.State == LocalBlockStateHot {
		if err := removePreparedEvictionMarker(s.blocksDir, selected.BlockID); err != nil {
			return failedApplyBlock(selected, err)
		}
	}
	return skippedApplyBlock(selected, eviction.SkipReasonLeaderHotCopyRequired)
}

func removePreparedEvictionMarker(blocksDir string, blockID uint64) error {
	if err := os.Remove(EvictionMarkerPath(blocksDir, blockID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove prepared eviction marker: %w", err)
	}
	if err := syncDirectory(blocksDir); err != nil {
		return fmt.Errorf("sync blocks directory after marker cleanup: %w", err)
	}
	return nil
}

func (s *Shard) unlinkEvictedBlockIfFollower(selected eviction.PlanBlock, lifecycle LocalBlockLifecycle) (bool, eviction.ApplyBlock, error) {
	if s.leaderHotCopyRequired() {
		return false, s.skipEvictionAfterPreparedMarker(selected, lifecycle), nil
	}
	removed, err := s.unlinkEvictedBlock(selected.BlockID)
	return removed, eviction.ApplyBlock{}, err
}

func (s *Shard) unlinkEvictedBlock(blockID uint64) (bool, error) {
	if err := os.Remove(block.FilePath(s.blocksDir, blockID)); err != nil {
		return false, fmt.Errorf("remove Block: %w", err)
	}
	if err := syncDirectory(s.blocksDir); err != nil {
		return true, fmt.Errorf("sync blocks directory: %w", err)
	}
	return true, nil
}

func (s *Shard) evictionApplySkipReason(selected eviction.PlanBlock, lifecycle LocalBlockLifecycle, nowUs int64) string {
	if selected.ShardID != s.shardID {
		return eviction.SkipReasonShardFilter
	}
	if lifecycle.State != LocalBlockStateHot && lifecycle.State != LocalBlockStateHotCleanupNeeded {
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

func restoredHotResidencyApplies(windowSeconds int64, lifecycle LocalBlockLifecycle, nowUs int64) bool {
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
