package shard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	raftpb "go.etcd.io/raft/v3/raftpb"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const evictionApplyTestShardID = 7

func TestApplyEvictionPlanWritesMarkerBeforeRemovingBlock(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	assertEvictionApplyCompleted(t, result)
	assertBlockEvictedForApply(t, s, 1)

	second, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan second call: %v", err)
	}
	if second.CompletedAtUs != result.CompletedAtUs || second.BytesFreed != result.BytesFreed {
		t.Fatalf("second result = %+v, want stored result %+v", second, result)
	}
}

func TestApplyEvictionPlanReportsCompletedWithSkipsForDrift(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageHotConfirmedBlockForEvictionApply(t, s, 2, 2048)
	stageAlreadyEvictedBlockForEvictionApply(t, s, 2)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected = append(plan.Selected, eviction.PlanBlock{
		BlockID:    2,
		ShardID:    evictionApplyTestShardID,
		SizeBytes:  2048,
		BackendKey: "cell-a/shards/0000000000000007/0000000000000002.blk",
		LocalState: string(LocalBlockStateHot),
	})
	s.evictionPlans[plan.PlanID] = plan

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusCompletedWithSkips {
		t.Fatalf("status = %s, want completed_with_skips", result.Status)
	}
	if result.EvictedBlocks != 1 || result.SkippedBlocks != 1 || result.BytesFreed != 1024 {
		t.Fatalf("result totals = %+v, want one eviction and one skip", result)
	}
	if result.Blocks[1].Status != eviction.ApplyBlockStatusSkipped || result.Blocks[1].Reason != eviction.SkipReasonLocalStateNotHot {
		t.Fatalf("second Block result = %+v, want local_state_not_hot skip", result.Blocks[1])
	}
}

func TestApplyEvictionPlanReportsNoEffectWhenAllBlocksDrift(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageAlreadyEvictedBlockForEvictionApply(t, s, 1)
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusNoEffect {
		t.Fatalf("status = %s, want no_effect", result.Status)
	}
	if result.EvictedBlocks != 0 || result.SkippedBlocks != 1 || result.BytesFreed != 0 {
		t.Fatalf("result totals = %+v, want no eviction and one skip", result)
	}
}

func TestApplyEvictionPlanStopsWhenDisabled(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, false)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if !errors.Is(err, eviction.ErrApplyDisabled) {
		t.Fatalf("ApplyEvictionPlan error = %v, want ErrApplyDisabled", err)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after disabled apply: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
}

func TestApplyEvictionPlanRejectsMissingRestoreBackend(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	s.upload.Backend = nil
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ApplyEvictionPlan error = %v, want ErrUnavailable", err)
	}
	if reason, ok := storeapi.UnavailableReason(err); !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("unavailable reason = %q, %v; want backend_restore_unavailable", reason, ok)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after backend unavailable: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
}

func TestApplyEvictionPlanRequiresCommittedAuthorityBeforeUnlink(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockCatalogOnlyForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if result.FailedBlocks != 1 || len(result.Blocks) != 1 {
		t.Fatalf("result = %+v, want one failed Block", result)
	}
	if !strings.Contains(result.Blocks[0].Error, index.ErrConfirmedUploadNotFound.Error()) {
		t.Fatalf("block error = %q, want ErrConfirmedUploadNotFound", result.Blocks[0].Error)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot without committed authority: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
}

func TestApplyEvictionPlanSkipsFreshlyRestoredBlock(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	s.eviction.HotResidencyWindow = time.Hour
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	if err := WriteRestoreMarker(s.blocksDir, RestoreMarker{
		BlockID:      1,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       RestoreSourceBackend,
		Reason:       RestoreReasonRead,
	}); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusNoEffect || result.SkippedBlocks != 1 {
		t.Fatalf("result = %+v, want no_effect with one skip", result)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Reason != eviction.SkipReasonHotResidencyWindow {
		t.Fatalf("block result = %+v, want hot residency skip", result.Blocks)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after restored residency skip: %v", err)
	}
}

func TestApplyEvictionPlanDoesNotCacheAllSkipResult(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	s.eviction.HotResidencyWindow = time.Hour
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	if err := WriteRestoreMarker(s.blocksDir, RestoreMarker{
		BlockID:      1,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       RestoreSourceBackend,
		Reason:       RestoreReasonRead,
	}); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	if result.Status != eviction.ApplyStatusNoEffect || result.SkippedBlocks != 1 {
		t.Fatalf("result = %+v, want no_effect with one skip", result)
	}
	if _, ok := s.evictionApplyResults[plan.PlanID]; ok {
		t.Fatal("all-skip apply result should not be cached")
	}

	s.eviction.HotResidencyWindow = 0
	retry, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan retry: %v", err)
	}
	assertEvictionApplyCompleted(t, retry)
	assertBlockEvictedForApply(t, s, 1)
}

func TestApplyEvictionPlanUsesCurrentHotResidencyWindow(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	s.eviction.HotResidencyWindow = time.Hour
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	if err := WriteRestoreMarker(s.blocksDir, RestoreMarker{
		BlockID:      1,
		RestoredAtUs: time.Now().Add(-30 * time.Second).UTC().UnixMicro(),
		Source:       RestoreSourceBackend,
		Reason:       RestoreReasonRead,
	}); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)
	plan.Config.HotResidencyWindowSeconds = 1
	s.evictionPlans[plan.PlanID] = plan

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusNoEffect || result.SkippedBlocks != 1 {
		t.Fatalf("result = %+v, want no_effect with one current-config residency skip", result)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Reason != eviction.SkipReasonHotResidencyWindow {
		t.Fatalf("block result = %+v, want hot residency skip", result.Blocks)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after current residency skip: %v", err)
	}
}

func TestApplyEvictionPlanRetriesHotCleanupNeededBlock(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	confirmed, err := s.idx.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	assertEvictionApplyCompleted(t, result)
	assertBlockEvictedForApply(t, s, 1)
}

func TestApplyEvictionPlanRejectsInvalidPlanState(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*Shard, eviction.Plan) string
		want   error
	}{
		{
			name: "missing",
			mutate: func(_ *Shard, _ eviction.Plan) string {
				return "missing"
			},
			want: eviction.ErrPlanNotFound,
		},
		{
			name: "expired",
			mutate: func(s *Shard, plan eviction.Plan) string {
				plan.ExpiresAtUs = time.Now().Add(-time.Second).UnixMicro()
				s.evictionPlans[plan.PlanID] = plan
				return plan.PlanID
			},
			want: eviction.ErrPlanExpired,
		},
		{
			name: "stale member identity",
			mutate: func(s *Shard, plan eviction.Plan) string {
				plan.MemberID = "old-member"
				s.evictionPlans[plan.PlanID] = plan
				return plan.PlanID
			},
			want: eviction.ErrPlanStale,
		},
		{
			name: "already in progress",
			mutate: func(s *Shard, plan eviction.Plan) string {
				s.evictionApplyRunning[plan.PlanID] = struct{}{}
				return plan.PlanID
			},
			want: eviction.ErrApplyInProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := shardForEvictionApplyTest(t, true)
			stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
			plan := storeEvictionApplyPlan(t, s)
			planID := tt.mutate(s, plan)

			_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: planID})
			if !errors.Is(err, tt.want) {
				t.Fatalf("ApplyEvictionPlan error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestApplyEvictionPlanRejectsRebuildInProgress(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	s.rebuilder = newProjectionRebuilder(&projectionRebuildCoreStub{}, t.TempDir(), s.blocksDir, s.shardID, UploadConfig{}, nil)
	s.rebuilder.setInProgressForTest(true)

	_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if !errors.Is(err, storeapi.ErrRebuilding) {
		t.Fatalf("ApplyEvictionPlan error = %v, want ErrRebuilding", err)
	}
}

func TestTriggerRebuildReportsInProgressDuringEvictionApply(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	core := &projectionRebuildCoreStub{swapStarted: make(chan struct{})}
	s.rebuilder = newProjectionRebuilder(core, t.TempDir(), s.blocksDir, s.shardID, UploadConfig{}, nil)
	s.evictionApplyRunning["plan-apply-1"] = struct{}{}

	alreadyInProgress, err := s.TriggerRebuild(ctx)
	if err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	if !alreadyInProgress {
		t.Fatal("TriggerRebuild should report already in progress during eviction apply")
	}
	if s.rebuilder.InProgress() {
		t.Fatal("rebuild should not start while eviction apply is running")
	}
	select {
	case <-core.swapStarted:
		t.Fatal("rebuild swap should not start while eviction apply is running")
	default:
	}
}

func TestApplyEvictionPlanRejectsExpiredCachedResult(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	plan.ExpiresAtUs = time.Now().Add(-time.Second).UnixMicro()
	s.evictionPlans[plan.PlanID] = plan
	s.evictionApplyResults[plan.PlanID] = eviction.ApplyResult{
		PlanID: plan.PlanID,
		Status: eviction.ApplyStatusCompleted,
	}

	_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if !errors.Is(err, eviction.ErrPlanExpired) {
		t.Fatalf("ApplyEvictionPlan error = %v, want ErrPlanExpired", err)
	}
	if _, ok := s.evictionApplyResults[plan.PlanID]; ok {
		t.Fatal("expired cached result should be pruned")
	}
}

func TestEvictionPlanStatusReportsPendingPlan(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	status, err := s.EvictionPlanStatus(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("EvictionPlanStatus pending: %v", err)
	}
	if status.Status != eviction.PlanStatusPending || status.Plan == nil || status.Plan.PlanID != plan.PlanID {
		t.Fatalf("pending status = %+v, want pending with plan", status)
	}
}

func TestEvictionPlanStatusReportsRunningPlan(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	s.evictionApplyRunning[plan.PlanID] = struct{}{}
	status, err := s.EvictionPlanStatus(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("EvictionPlanStatus running: %v", err)
	}
	if status.Status != eviction.PlanStatusRunning {
		t.Fatalf("running status = %+v, want running", status)
	}
}

func TestEvictionPlanStatusReportsCompletedResult(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	s.evictionApplyResults[plan.PlanID] = eviction.ApplyResult{
		PlanID: plan.PlanID,
		Status: eviction.ApplyStatusCompleted,
	}
	status, err := s.EvictionPlanStatus(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("EvictionPlanStatus completed: %v", err)
	}
	if status.Status != eviction.ApplyStatusCompleted || status.ApplyResult == nil || status.ApplyResult.PlanID != plan.PlanID {
		t.Fatalf("completed status = %+v, want completed apply result", status)
	}
}

func TestApplyEvictionPlanFailsBlockWhenConfirmationDrifts(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected[0].SizeBytes = 2048
	s.evictionPlans[plan.PlanID] = plan

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusFailed || result.FailedBlocks != 1 {
		t.Fatalf("result = %+v, want failed one Block", result)
	}
	if len(result.Blocks) != 1 || !strings.Contains(result.Blocks[0].Error, "size mismatch") {
		t.Fatalf("block result = %+v, want size mismatch failure", result.Blocks)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain after failed apply: %v", err)
	}
}

func TestApplyEvictionPlanStopsAtFirstBlockFailure(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageHotConfirmedBlockForEvictionApply(t, s, 2, 2048)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected[0].SizeBytes = 2048
	plan.Selected = append(plan.Selected, eviction.PlanBlock{
		BlockID:    2,
		ShardID:    evictionApplyTestShardID,
		SizeBytes:  2048,
		BackendKey: "cell-a/shards/0000000000000007/0000000000000002.blk",
		LocalState: string(LocalBlockStateHot),
	})
	s.evictionPlans[plan.PlanID] = plan

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusFailed || result.FailedBlocks != 1 {
		t.Fatalf("result = %+v, want failed one Block", result)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].BlockID != 1 {
		t.Fatalf("blocks = %+v, want only first failed Block", result.Blocks)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 2)); err != nil {
		t.Fatalf("second Block should remain untouched after first failure: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second eviction marker stat error = %v, want not exist", err)
	}

	assertCachedEvictionApplyResult(t, s, plan.PlanID, result)
}

func TestApplyEvictionPlanCachesFailedResultAfterEvictionSideEffect(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageHotConfirmedBlockForEvictionApply(t, s, 2, 2048)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected = append(plan.Selected, eviction.PlanBlock{
		BlockID:    2,
		ShardID:    evictionApplyTestShardID,
		SizeBytes:  4096,
		BackendKey: "cell-a/shards/0000000000000007/0000000000000002.blk",
		LocalState: string(LocalBlockStateHot),
	})
	s.evictionPlans[plan.PlanID] = plan

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	if result.Status != eviction.ApplyStatusFailed || result.EvictedBlocks != 1 || result.FailedBlocks != 1 {
		t.Fatalf("result = %+v, want one eviction then one failure", result)
	}

	assertCachedEvictionApplyResult(t, s, plan.PlanID, result)
}

func TestApplyEvictionPlanDoesNotCacheCanceledResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	result, cacheable := s.applyEvictionPlanBlocks(ctx, plan)
	s.finishEvictionApply(plan.PlanID, result, cacheable)

	if cacheable {
		t.Fatal("canceled apply result should not be cacheable")
	}
	if _, ok := s.evictionApplyResults[plan.PlanID]; ok {
		t.Fatal("canceled apply result should not be cached")
	}
	retry, err := s.ApplyEvictionPlan(context.Background(), eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan retry: %v", err)
	}
	assertEvictionApplyCompleted(t, retry)
}

func TestSkipEvictionAfterPreparedMarkerRemovesMarkerWrittenByApply(t *testing.T) {
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	confirmed, err := s.idx.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)
	if err := s.prepareEvictionMarkerForApply(plan, LocalBlockLifecycle{State: LocalBlockStateHot}, confirmed); err != nil {
		t.Fatalf("prepareEvictionMarkerForApply: %v", err)
	}

	result := s.skipEvictionAfterPreparedMarker(plan.Selected[0], LocalBlockLifecycle{State: LocalBlockStateHot})

	if result.Status != eviction.ApplyBlockStatusSkipped || result.Reason != eviction.SkipReasonLeaderHotCopyRequired {
		t.Fatalf("skip result = %+v, want leader hot-copy skip", result)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after leadership skip: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
}

func TestSkipEvictionAfterPreparedMarkerPreservesCrashCleanupMarker(t *testing.T) {
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	confirmed, err := s.idx.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	plan := storeEvictionApplyPlan(t, s)

	result := s.skipEvictionAfterPreparedMarker(plan.Selected[0], LocalBlockLifecycle{State: LocalBlockStateHotCleanupNeeded})

	if result.Status != eviction.ApplyBlockStatusSkipped || result.Reason != eviction.SkipReasonLeaderHotCopyRequired {
		t.Fatalf("skip result = %+v, want leader hot-copy skip", result)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); err != nil {
		t.Fatalf("pre-existing crash cleanup marker should remain: %v", err)
	}
}

func TestApplyEvictionPlanFencesFollowerCheckThroughUnlink(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	raft := &evictionApplyRaftStub{becomeLeaderBeforeMutation: true}
	s.raft = raft
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if raft.stableLeadershipCalls != 1 {
		t.Fatalf("stable leadership calls = %d, want 1", raft.stableLeadershipCalls)
	}
	if result.Status != eviction.ApplyStatusNoEffect || result.SkippedBlocks != 1 {
		t.Fatalf("result = %+v, want retryable no_effect", result)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Reason != eviction.SkipReasonLeaderHotCopyRequired {
		t.Fatalf("block result = %+v, want leader hot-copy skip", result.Blocks)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, 1)); err != nil {
		t.Fatalf("Block should remain hot after fenced leadership change: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
}

func TestApplyEvictionPlanUsesDefaultMarkerReason(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	plan.Reason = ""
	s.evictionPlans[plan.PlanID] = plan

	if _, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID}); err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	marker, err := ReadEvictionMarker(s.blocksDir, 1)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if marker.Reason != EvictionReasonEvidenceRun {
		t.Fatalf("marker reason = %s, want evidence_run", marker.Reason)
	}
}

func TestValidateEvictionApplyAuthorityRejectsMismatches(t *testing.T) {
	confirmed := confirmedUploadForEvictionApply(1, 1024)
	selected := eviction.PlanBlock{
		BlockID:    1,
		ShardID:    confirmed.ShardID,
		SizeBytes:  confirmed.BlockObject.SizeBytes,
		BackendKey: confirmed.BlockObject.Key,
	}

	tests := []struct {
		name   string
		mutate func(eviction.PlanBlock) eviction.PlanBlock
	}{
		{
			name: "shard",
			mutate: func(block eviction.PlanBlock) eviction.PlanBlock {
				block.ShardID++
				return block
			},
		},
		{
			name: "size",
			mutate: func(block eviction.PlanBlock) eviction.PlanBlock {
				block.SizeBytes++
				return block
			},
		},
		{
			name: "backend key",
			mutate: func(block eviction.PlanBlock) eviction.PlanBlock {
				block.BackendKey = "other.blk"
				return block
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateEvictionApplyAuthority(tt.mutate(selected), confirmed); err == nil {
				t.Fatal("validateEvictionApplyAuthority succeeded, want error")
			}
		})
	}
}

func TestEvictionMarkerBeforeUnlinkCrashClassifiesHotCleanupNeeded(t *testing.T) {
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	confirmed, err := s.idx.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}

	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         1,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	lifecycle, err := ClassifyLocalBlock(s.blocksDir, 1)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != LocalBlockStateHotCleanupNeeded || !lifecycle.ServingAllowed || !lifecycle.HealthDegraded {
		t.Fatalf("lifecycle = %+v, want hot_cleanup_needed serving allowed degraded", lifecycle)
	}

	if err := CleanupHotLifecycleMarkers(s.blocksDir); err != nil {
		t.Fatalf("CleanupHotLifecycleMarkers: %v", err)
	}
	lifecycle, err = ClassifyLocalBlock(s.blocksDir, 1)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock after cleanup: %v", err)
	}
	if lifecycle.State != LocalBlockStateHot {
		t.Fatalf("lifecycle after cleanup = %+v, want hot", lifecycle)
	}
}

func shardForEvictionApplyTest(t *testing.T, enabled bool) *Shard {
	t.Helper()

	idx := openApplyTestIndex(t)
	blocksDir := t.TempDir()
	return &Shard{
		blocksDir:            blocksDir,
		shardID:              evictionApplyTestShardID,
		idx:                  idx,
		upload:               UploadConfig{Backend: noopRebuildBackend{}},
		eviction:             EvictionConfig{Enabled: enabled, PlanTTL: time.Minute},
		memberHostname:       "scrapd-2",
		memberID:             "member-b",
		evictionPlans:        make(map[string]eviction.Plan),
		evictionApplyResults: make(map[string]eviction.ApplyResult),
		evictionApplyRunning: make(map[string]struct{}),
	}
}

func stageAlreadyEvictedBlockForEvictionApply(t *testing.T, s *Shard, blockID uint64) {
	t.Helper()

	confirmed, err := s.idx.GetConfirmedUpload(blockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         blockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("remove Block: %v", err)
	}
}

func stageHotConfirmedBlockForEvictionApply(t *testing.T, s *Shard, blockID uint64, sizeBytes int64) {
	t.Helper()

	confirmed := stageHotConfirmedBlockCatalogOnlyForEvictionApply(t, s, blockID, sizeBytes)
	if err := writeConfirmedUploadAuthority(s.blocksDir, confirmed); err != nil {
		t.Fatalf("writeConfirmedUploadAuthority: %v", err)
	}
}

func stageHotConfirmedBlockCatalogOnlyForEvictionApply(t *testing.T, s *Shard, blockID uint64, sizeBytes int64) index.ConfirmedUpload {
	t.Helper()

	if err := os.WriteFile(block.FilePath(s.blocksDir, blockID), []byte("block bytes"), 0o600); err != nil {
		t.Fatalf("write Block: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(s.blocksDir, blockID), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}
	confirmed := confirmedUploadForEvictionApply(blockID, sizeBytes)
	if err := s.idx.PutConfirmedUpload(confirmed); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}
	return confirmed
}

func storeEvictionApplyPlan(t *testing.T, s *Shard) eviction.Plan {
	t.Helper()

	now := time.Now().UTC()
	plan := eviction.Plan{
		PlanID:         "plan-apply-1",
		GeneratedAtUs:  now.UnixMicro(),
		ExpiresAtUs:    now.Add(time.Minute).UnixMicro(),
		MemberHostname: "scrapd-2",
		MemberID:       "member-b",
		Reason:         eviction.ReasonEvidenceRun,
		Selected: []eviction.PlanBlock{{
			BlockID:    1,
			ShardID:    evictionApplyTestShardID,
			SizeBytes:  1024,
			BackendKey: "cell-a/shards/0000000000000007/0000000000000001.blk",
			LocalState: string(LocalBlockStateHot),
		}},
	}
	s.evictionPlans[plan.PlanID] = plan
	return plan
}

func confirmedUploadForEvictionApply(blockID uint64, sizeBytes int64) index.ConfirmedUpload {
	blockSuffix := fmt.Sprintf("%016x", blockID)
	return index.ConfirmedUpload{
		BlockID:         blockID,
		ShardID:         evictionApplyTestShardID,
		ConfirmedAtUs:   time.Now().Add(-time.Hour).UnixMicro(),
		SealedSizeBytes: sizeBytes,
		BlockObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/" + blockSuffix + ".blk",
			SizeBytes:       sizeBytes,
			ValidationToken: "block-validation",
		},
		IndexObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/" + blockSuffix + ".idx",
			SizeBytes:       256,
			ValidationToken: "index-validation",
		},
	}
}

func assertEvictionApplyCompleted(t *testing.T, result eviction.ApplyResult) {
	t.Helper()

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.BytesFreed != 1024 || result.EvictedBlocks != 1 {
		t.Fatalf("result totals = %+v, want bytes_freed=1024 evicted_blocks=1", result)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Status != eviction.ApplyBlockStatusEvicted {
		t.Fatalf("blocks = %+v, want one evicted Block", result.Blocks)
	}
}

func assertCachedEvictionApplyResult(t *testing.T, s *Shard, planID string, want eviction.ApplyResult) {
	t.Helper()

	got, err := s.ApplyEvictionPlan(context.Background(), eviction.ApplyRequest{PlanID: planID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan cached call: %v", err)
	}
	if got.CompletedAtUs != want.CompletedAtUs || len(got.Blocks) != len(want.Blocks) {
		t.Fatalf("cached result = %+v, want %+v", got, want)
	}
}

func assertBlockEvictedForApply(t *testing.T, s *Shard, blockID uint64) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(s.blocksDir, blockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("Block index should remain: %v", err)
	}
	marker, err := ReadEvictionMarker(s.blocksDir, blockID)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if marker.BackendKey == "" || marker.ValidationToken == "" {
		t.Fatalf("eviction marker missing Backend authority: %+v", marker)
	}
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != LocalBlockStateEvicted {
		t.Fatalf("lifecycle state = %s, want evicted", lifecycle.State)
	}
}

type evictionApplyRaftStub struct {
	leader                     bool
	becomeLeaderBeforeMutation bool
	stableLeadershipCalls      int
}

func (r *evictionApplyRaftStub) Propose(context.Context, []byte) error {
	return nil
}

func (r *evictionApplyRaftStub) ReadIndex(context.Context) (uint64, error) {
	return 0, nil
}

func (r *evictionApplyRaftStub) Step(context.Context, raftpb.Message) error {
	return nil
}

func (r *evictionApplyRaftStub) IsLeader() bool {
	return r.leader
}

func (r *evictionApplyRaftStub) LeaderID() uint64 {
	if r.leader {
		return 1
	}
	return 2
}

func (r *evictionApplyRaftStub) AppliedIndex() uint64 {
	return 0
}

func (r *evictionApplyRaftStub) CommitIndex() uint64 {
	return 0
}

func (r *evictionApplyRaftStub) WithStableLeadership(fn func() error) error {
	r.stableLeadershipCalls++
	if r.becomeLeaderBeforeMutation {
		r.leader = true
	}
	return fn()
}

func (r *evictionApplyRaftStub) Stop() {}
