package shard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
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

	assertEvictionApplyCompleted(t, result, 1024)
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

	if err := os.WriteFile(block.FilePath(s.blocksDir, blockID), []byte("block bytes"), 0o600); err != nil {
		t.Fatalf("write Block: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(s.blocksDir, blockID), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}
	if err := s.idx.PutConfirmedUpload(confirmedUploadForEvictionApply(blockID, sizeBytes)); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}
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

func assertEvictionApplyCompleted(t *testing.T, result eviction.ApplyResult, bytesFreed int64) {
	t.Helper()

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.BytesFreed != bytesFreed || result.EvictedBlocks != 1 {
		t.Fatalf("result totals = %+v, want bytes_freed=%d evicted_blocks=1", result, bytesFreed)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Status != eviction.ApplyBlockStatusEvicted {
		t.Fatalf("blocks = %+v, want one evicted Block", result.Blocks)
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
