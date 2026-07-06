package shard

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"go.etcd.io/raft/v3/raftpb"

	"github.com/petabytecl/scrap/internal/backend"
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

func TestApplyEvictionPlanPreservesIndexMetadataReads(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := shardForEvictionApplyTest(t, true)
	raft := &evictionApplyRaftStub{leader: false}
	s.raft = raft
	s.rebuilder = newProjectionRebuilder(&projectionRebuildCoreStub{}, t.TempDir(), s.blocksDir, s.shardID, UploadConfig{}, nil)
	content := bytes.Repeat([]byte("metadata after eviction "), 3)
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, content)
	confirmedBefore := confirmedUploadForAssertion(t, s, confirmed.BlockID)
	backendProbe := &recordingEvictionApplyBackend{delegate: backendStore}
	s.upload.Backend = backendProbe
	plan := storeEvictionApplyPlanForConfirmedUploads(t, s, eviction.ReasonEvidenceRun, 0, confirmed)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	assertEvictionApplyPreservedMetadataResult(t, result, confirmed.BlockObject.SizeBytes)
	assertBlockEvictedForApply(t, s, confirmed.BlockID)
	assertEvictionApplyPreservedUploadAuthority(t, s, confirmedBefore)
	assertEvictionApplyPreservedMetadataReads(ctx, t, s, raft, confirmed.BlockID, content)
	assertEvictionApplyBackendUnused(t, backendProbe)
}

func assertEvictionApplyPreservedMetadataResult(t *testing.T, result eviction.ApplyResult, wantBytesFreed int64) {
	t.Helper()

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.EvictedBlocks != 1 {
		t.Fatalf("evicted_blocks = %d, want 1", result.EvictedBlocks)
	}
	if result.ValidatedBlocks != 0 {
		t.Fatalf("validated_blocks = %d, want 0", result.ValidatedBlocks)
	}
	if result.BytesFreed != wantBytesFreed {
		t.Fatalf("bytes_freed = %d, want %d", result.BytesFreed, wantBytesFreed)
	}
}

func assertEvictionApplyPreservedUploadAuthority(t *testing.T, s *Shard, want index.ConfirmedUpload) {
	t.Helper()

	got, err := s.idx.GetConfirmedUpload(want.BlockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload after apply: %v", err)
	}
	if got != want {
		t.Fatalf("confirmed upload after apply = %+v, want unchanged %+v", got, want)
	}
	if _, err := s.idx.GetPendingUpload(want.BlockID); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload after apply error = %v, want ErrPendingUploadNotFound", err)
	}
}

func assertEvictionApplyPreservedMetadataReads(
	ctx context.Context,
	t *testing.T,
	s *Shard,
	raft *evictionApplyRaftStub,
	blockID uint64,
	content []byte,
) {
	t.Helper()

	raft.leader = true
	meta, err := s.HeadDocument(ctx, evictionApplyTxID(blockID), "doc-1.bin")
	if err != nil {
		t.Fatalf("HeadDocument after eviction apply: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("HeadDocument size = %d, want %d", meta.Size, len(content))
	}
	docs, err := s.FindDocuments(ctx, evictionApplyTxID(blockID))
	if err != nil {
		t.Fatalf("FindDocuments after eviction apply: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("FindDocuments after eviction apply = %+v, want one Document", docs)
	}
	if docs[0].Name != "doc-1.bin" {
		t.Fatalf("FindDocuments name = %q, want doc-1.bin", docs[0].Name)
	}
	if _, err := os.Stat(block.FilePath(s.blocksDir, blockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat after metadata reads = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("Block index after metadata reads: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("eviction marker after metadata reads: %v", err)
	}
}

func assertEvictionApplyBackendUnused(t *testing.T, backendProbe *recordingEvictionApplyBackend) {
	t.Helper()

	if backendProbe.heads != 0 || backendProbe.gets != 0 || backendProbe.lists != 0 {
		t.Fatalf("metadata reads touched Backend: heads=%d gets=%d lists=%d", backendProbe.heads, backendProbe.gets, backendProbe.lists)
	}
}

func TestCreateEvictionPlanDoesNotMutateLocalBlockState(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := shardForEvictionApplyTest(t, true)
	raft := &evictionApplyRaftStub{leader: true}
	s.raft = raft
	s.rebuilder = newProjectionRebuilder(&projectionRebuildCoreStub{}, t.TempDir(), s.blocksDir, s.shardID, UploadConfig{}, nil)
	content := bytes.Repeat([]byte("metadata visible after dry-run "), 2)
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, content)
	confirmedBefore := confirmedUploadForAssertion(t, s, confirmed.BlockID)
	blockPath := block.FilePath(s.blocksDir, confirmed.BlockID)
	indexPath := block.IdxFilePath(s.blocksDir, confirmed.BlockID)
	beforeBlock := readEvictionApplyFile(t, blockPath)
	beforeIndex := readEvictionApplyFile(t, indexPath)
	assertEvictionApplyMetadataVisible(ctx, t, s, confirmed.BlockID, content)

	raft.leader = false
	plan, err := s.CreateEvictionPlan(ctx, eviction.PlanRequest{
		MemberHostname: "scrapd-2",
		Reason:         eviction.ReasonEvidenceRun,
	})
	if err != nil {
		t.Fatalf("CreateEvictionPlan: %v", err)
	}

	assertDryRunSelectedConfirmedBlock(t, plan, confirmed)
	assertDryRunPlanEvidence(t, plan)
	assertDryRunPreservedLocalBlockFiles(t, s, confirmed.BlockID, blockPath, indexPath, beforeBlock, beforeIndex)
	assertEvictionApplyPreservedUploadAuthority(t, s, confirmedBefore)
	raft.leader = true
	assertEvictionApplyMetadataVisible(ctx, t, s, confirmed.BlockID, content)
}

func assertDryRunSelectedConfirmedBlock(t *testing.T, plan eviction.Plan, confirmed index.ConfirmedUpload) {
	t.Helper()

	if len(plan.Selected) != 1 {
		t.Fatalf("selected Blocks = %+v, want one eligible Block", plan.Selected)
	}
	selected := plan.Selected[0]
	if selected.BlockID != confirmed.BlockID {
		t.Fatalf("selected Block ID = %d, want %d", selected.BlockID, confirmed.BlockID)
	}
	if selected.ShardID != confirmed.ShardID {
		t.Fatalf("selected Shard ID = %d, want %d", selected.ShardID, confirmed.ShardID)
	}
	if selected.SizeBytes != confirmed.SealedSizeBytes {
		t.Fatalf("selected size = %d, want %d", selected.SizeBytes, confirmed.SealedSizeBytes)
	}
	if selected.LocalState != string(LocalBlockStateHot) {
		t.Fatalf("selected local state = %q, want hot", selected.LocalState)
	}
	assertDryRunSelectedTiming(t, selected)
}

func assertDryRunPlanEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	assertDryRunPlanIdentityEvidence(t, plan)
	assertDryRunPlanConfigEvidence(t, plan)
	assertDryRunPlanSelectionEvidence(t, plan)
	assertDryRunPlanSkipEvidence(t, plan)
}

func assertDryRunPlanIdentityEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if plan.PlanID == "" {
		t.Fatal("plan ID is empty")
	}
	if plan.MemberHostname != "scrapd-2" || plan.MemberID != "member-b" {
		t.Fatalf("plan member identity = %s/%s, want scrapd-2/member-b", plan.MemberHostname, plan.MemberID)
	}
}

func assertDryRunPlanConfigEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if plan.Config.PlanTTLSeconds == 0 || plan.Config.RecommendedMaxBlocks == 0 {
		t.Fatalf("plan config evidence = %+v, want active config snapshot", plan.Config)
	}
}

func assertDryRunPlanSelectionEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if plan.CandidateBlocks != 1 || plan.EligibleBlocks != 1 || plan.SelectedBytes == 0 {
		t.Fatalf("plan candidate evidence = %+v, want one selected eligible Block", plan)
	}
}

func assertDryRunPlanSkipEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if len(plan.Skipped) != 0 || len(plan.SkipCountsByReason) != 0 {
		t.Fatalf("plan skip evidence = skipped %+v counts %+v, want no skips", plan.Skipped, plan.SkipCountsByReason)
	}
}

func assertEvictionApplyMetadataVisible(
	ctx context.Context,
	t *testing.T,
	s *Shard,
	blockID uint64,
	content []byte,
) {
	t.Helper()

	meta, err := s.HeadDocument(ctx, evictionApplyTxID(blockID), "doc-1.bin")
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("HeadDocument size = %d, want %d", meta.Size, len(content))
	}
	docs, err := s.FindDocuments(ctx, evictionApplyTxID(blockID))
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "doc-1.bin" {
		t.Fatalf("FindDocuments = %+v, want doc-1.bin", docs)
	}
}

func assertDryRunSelectedTiming(t *testing.T, selected eviction.PlanBlock) {
	t.Helper()

	if selected.ConfirmedAtUs == 0 {
		t.Fatalf("selected confirmed_at_us = 0, want evidence timestamp")
	}
	if selected.EligibleAtUs == 0 {
		t.Fatalf("selected eligible_at_us = 0, want evidence timestamp")
	}
	if selected.HotResidencyWindowSeconds != 0 {
		t.Fatalf("hot residency window = %d, want 0", selected.HotResidencyWindowSeconds)
	}
}

func assertDryRunPreservedLocalBlockFiles(
	t *testing.T,
	s *Shard,
	blockID uint64,
	blockPath string,
	indexPath string,
	beforeBlock []byte,
	beforeIndex []byte,
) {
	t.Helper()

	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, blockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist after dry-run", err)
	}
	if got := readEvictionApplyFile(t, blockPath); !bytes.Equal(got, beforeBlock) {
		t.Fatal("dry-run mutated Block data")
	}
	if got := readEvictionApplyFile(t, indexPath); !bytes.Equal(got, beforeIndex) {
		t.Fatal("dry-run mutated Block index")
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
	storeEvictionPlanForTest(s, plan)

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

func TestApplyEvictionPlanRecordsApplySkipReasons(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	metrics := &recordingEvictionMetrics{}
	s.evictionMetrics = metrics
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
	storeEvictionPlanForTest(s, plan)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusCompletedWithSkips {
		t.Fatalf("status = %s, want completed_with_skips", result.Status)
	}
	if metrics.applySkipCounts[eviction.SkipReasonLocalStateNotHot] != 1 {
		t.Fatalf("apply skip counts = %+v, want local_state_not_hot=1", metrics.applySkipCounts)
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
	if hasEvictionApplyResultForTest(s, plan.PlanID) {
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
	storeEvictionPlanForTest(s, plan)

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

func TestApplyEvictionPlanValidatesEvidenceRunSample(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("validated content"))
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, eviction.ReasonEvidenceRun)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.EvictedBlocks != 1 || result.ValidatedBlocks != 1 || result.ValidationFailedBlocks != 0 {
		t.Fatalf("result = %+v, want one evicted and validated Block", result)
	}
	if result.BytesFreed != 0 {
		t.Fatalf("BytesFreed = %d, want 0 after validation restored sampled Block", result.BytesFreed)
	}
	if len(result.Validations) != 1 || result.Validations[0].Status != eviction.ValidationStatusPassed {
		t.Fatalf("validations = %+v, want one passed validation", result.Validations)
	}
	assertBlockRestoredByValidationForApply(t, s, 1)
}

func TestApplyEvictionPlanDefaultsEvidenceValidationToOneSample(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	first := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("first validated content"))
	second := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 2, []byte("second validated content"))
	third := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 3, []byte("not sampled content"))
	plan := storeEvictionApplyPlanForConfirmedUploads(t, s, eviction.ReasonEvidenceRun, 2, first, second, third)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.EvictedBlocks != 3 || result.ValidatedBlocks != 1 || result.ValidationFailedBlocks != 0 {
		t.Fatalf("result = %+v, want three evicted and only default sample validated", result)
	}
	if result.BytesFreed != second.BlockObject.SizeBytes+third.BlockObject.SizeBytes {
		t.Fatalf("BytesFreed = %d, want unsampled Blocks only", result.BytesFreed)
	}
	if len(result.Validations) != 1 || result.Validations[0].BlockID != 1 {
		t.Fatalf("validations = %+v, want Block 1 only", result.Validations)
	}
	assertBlockRestoredByValidationForApply(t, s, 1)
	assertBlockEvictedForApply(t, s, 2)
	assertBlockEvictedForApply(t, s, 3)
}

func TestApplyEvictionPlanSkipsValidationForNonEvidenceReason(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("not validated"))
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, "disk_recovery_drill")

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.ValidatedBlocks != 0 || len(result.Validations) != 0 {
		t.Fatalf("validation result = %+v, want no validation for non-evidence reason", result)
	}
	assertBlockEvictedForApply(t, s, 1)
}

func TestApplyEvictionPlanValidationFailsWhenProjectionCannotResolveDocument(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("projection mismatch"))
	if err := s.idx.Put(evictionApplyTxID(1), 99, 1, true); err != nil {
		t.Fatalf("corrupt projection: %v", err)
	}
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, eviction.ReasonEvidenceRun)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusEvictedWithValidationFailure {
		t.Fatalf("status = %s, want evicted_with_validation_failure", result.Status)
	}
	if result.ValidatedBlocks != 0 || result.ValidationFailedBlocks != 1 {
		t.Fatalf("result = %+v, want one validation failure", result)
	}
	if result.BytesFreed != confirmed.BlockObject.SizeBytes {
		t.Fatalf("BytesFreed = %d, want evicted Block bytes", result.BytesFreed)
	}
	assertBlockEvictedForApply(t, s, 1)
}

func TestApplyEvictionPlanValidationIgnoresClientCancellationAfterEviction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("cancelled client"))
	s.upload.Backend = cancelOnGetBackend{Backend: backendStore, cancel: cancel}
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, eviction.ReasonEvidenceRun)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if ctx.Err() == nil {
		t.Fatal("context was not canceled during validation")
	}
	if result.Status != eviction.ApplyStatusCompleted || result.ValidatedBlocks != 1 || result.ValidationFailedBlocks != 0 {
		t.Fatalf("result = %+v, want completed validation despite client cancellation", result)
	}
	assertCachedEvictionApplyResult(t, s, plan.PlanID, result)
}

func TestApplyEvictionPlanValidationUsesBoundedContextAfterClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("bounded validation"))
	s.upload.Backend = blockingGetBackend{Backend: backendStore, cancel: cancel}
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, eviction.ReasonEvidenceRun)
	plan.ExpiresAtUs = time.Now().Add(75 * time.Millisecond).UnixMicro()
	storeEvictionPlanForTest(s, plan)

	start := time.Now()
	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	elapsed := time.Since(start)

	if ctx.Err() == nil {
		t.Fatal("context was not canceled during validation")
	}
	if elapsed > time.Second {
		t.Fatalf("ApplyEvictionPlan took %s, want bounded validation to finish promptly", elapsed)
	}
	if result.Status != eviction.ApplyStatusEvictedWithValidationFailure || result.ValidationFailedBlocks != 1 {
		t.Fatalf("result = %+v, want bounded validation failure", result)
	}
	if len(result.Validations) != 1 || !strings.Contains(result.Validations[0].Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("validations = %+v, want context deadline exceeded", result.Validations)
	}
	if s.evictionCampaigns.HasRunningApply() {
		t.Fatal("eviction apply remained running after bounded validation timeout")
	}
}

func TestApplyEvictionPlanReportsValidationFailure(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	backendStore := backend.NewFS(t.TempDir())
	s.upload.Backend = backendStore
	confirmed := stageReadableHotConfirmedBlockForEvictionApply(ctx, t, s, backendStore, 1, []byte("missing validation source"))
	if err := backendStore.DeleteObject(ctx, confirmed.BlockObject.Key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	plan := storeEvictionApplyPlanForConfirmed(t, s, confirmed, eviction.ReasonEvidenceRun)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusEvictedWithValidationFailure {
		t.Fatalf("status = %s, want evicted_with_validation_failure", result.Status)
	}
	if result.EvictedBlocks != 1 || result.ValidatedBlocks != 0 || result.ValidationFailedBlocks != 1 {
		t.Fatalf("result = %+v, want one evicted Block and one validation failure", result)
	}
	if len(result.Validations) != 1 || result.Validations[0].Status != eviction.ValidationStatusFailed || result.Validations[0].Error == "" {
		t.Fatalf("validations = %+v, want one failed validation with error", result.Validations)
	}
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
				storeEvictionPlanForTest(s, plan)
				return plan.PlanID
			},
			want: eviction.ErrPlanExpired,
		},
		{
			name: "stale member identity",
			mutate: func(s *Shard, plan eviction.Plan) string {
				plan.MemberID = "old-member"
				storeEvictionPlanForTest(s, plan)
				return plan.PlanID
			},
			want: eviction.ErrPlanStale,
		},
		{
			name: "already in progress",
			mutate: func(s *Shard, plan eviction.Plan) string {
				beginEvictionApplyForTest(t, s, plan.PlanID)
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
	plan := storeEvictionApplyPlan(t, s)
	beginEvictionApplyForTest(t, s, plan.PlanID)

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
	storeEvictionPlanForTest(s, plan)
	finishEvictionApplyForTest(s, plan.PlanID, eviction.ApplyResult{
		PlanID: plan.PlanID,
		Status: eviction.ApplyStatusCompleted,
	})

	_, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if !errors.Is(err, eviction.ErrPlanExpired) {
		t.Fatalf("ApplyEvictionPlan error = %v, want ErrPlanExpired", err)
	}
	if hasEvictionApplyResultForTest(s, plan.PlanID) {
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

	beginEvictionApplyForTest(t, s, plan.PlanID)
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

	finishEvictionApplyForTest(s, plan.PlanID, eviction.ApplyResult{
		PlanID: plan.PlanID,
		Status: eviction.ApplyStatusCompleted,
	})
	status, err := s.EvictionPlanStatus(ctx, plan.PlanID)
	if err != nil {
		t.Fatalf("EvictionPlanStatus completed: %v", err)
	}
	if status.Status != eviction.ApplyStatusCompleted || status.ApplyResult == nil || status.ApplyResult.PlanID != plan.PlanID {
		t.Fatalf("completed status = %+v, want completed apply result", status)
	}
	if status.Plan == nil || status.Plan.PlanID != plan.PlanID || status.Plan.CandidateBlocks != plan.CandidateBlocks {
		t.Fatalf("completed plan evidence = %+v, want original plan", status.Plan)
	}
}

func TestEvictionHealthSnapshotSeparatesLocalLifecycleStates(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageEvictionHealthSnapshotStates(t, s)
	rebuildEvictionHealthForTest(t, s)

	got, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}

	assertEvictionHealthSnapshotCounts(t, got)
}

func stageEvictionHealthSnapshotStates(t *testing.T, s *Shard) {
	t.Helper()

	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageAlreadyEvictedBlockForEvictionApply(t, s, 1)
	stageHotConfirmedBlockForEvictionApply(t, s, 2, 2048)
	confirmed, err := s.idx.GetConfirmedUpload(2)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         2,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	stageHotConfirmedBlockForEvictionApply(t, s, 3, 4096)
	if err := os.Remove(block.IdxFilePath(s.blocksDir, 3)); err != nil {
		t.Fatalf("remove metadata-loss index: %v", err)
	}
	stageHotConfirmedBlockForEvictionApply(t, s, 4, 8192)
	if err := os.Remove(block.FilePath(s.blocksDir, 4)); err != nil {
		t.Fatalf("remove unexpected-loss Block: %v", err)
	}
	stageHotConfirmedBlockForEvictionApply(t, s, 5, 16384)
	if err := block.Quarantine(block.FilePath(s.blocksDir, 5)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	s.restoreFailuresByBlock = map[uint64]string{
		6: eviction.RestoreFailureBackendUnavailable,
		7: eviction.RestoreFailureBackendUnavailable,
	}
}

func assertEvictionHealthSnapshotCounts(t *testing.T, got eviction.HealthSnapshot) {
	t.Helper()

	if got.Pressure != eviction.HealthPressureDegraded {
		t.Fatalf("pressure = %s, want degraded", got.Pressure)
	}
	if got.EvictedBlocks != 1 || got.EvictedBytes != 1024 {
		t.Fatalf("evicted = %d/%d, want 1/1024", got.EvictedBlocks, got.EvictedBytes)
	}
	if got.HotCleanupNeededBlocks != 1 || got.MetadataLossBlocks != 1 || got.UnexpectedLossBlocks != 1 {
		t.Fatalf("lifecycle counts = cleanup:%d metadata:%d unexpected:%d, want 1/1/1",
			got.HotCleanupNeededBlocks, got.MetadataLossBlocks, got.UnexpectedLossBlocks)
	}
	if got.QuarantinedBlocks != 1 {
		t.Fatalf("quarantined = %d, want 1", got.QuarantinedBlocks)
	}
	if got.RestoreFailedBlocks != 2 || got.RestoreFailuresByReason[eviction.RestoreFailureBackendUnavailable] != 2 {
		t.Fatalf("restore failures = %+v/%d, want backend_restore_unavailable=2", got.RestoreFailuresByReason, got.RestoreFailedBlocks)
	}
}

func TestEvictionHealthSnapshotClearsRestoreFailureAfterSuccess(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)

	s.recordRestoreOutcome(1, RestoreReasonRead, storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "temporary"), time.Millisecond)
	failed, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot failed restore: %v", err)
	}
	if failed.RestoreFailedBlocks != 1 {
		t.Fatalf("restore failed blocks = %d, want 1", failed.RestoreFailedBlocks)
	}

	s.recordRestoreOutcome(1, RestoreReasonRead, nil, time.Millisecond)
	recovered, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot recovered restore: %v", err)
	}
	if recovered.RestoreFailedBlocks != 0 || len(recovered.RestoreFailuresByReason) != 0 {
		t.Fatalf("restore failures after success = %+v/%d, want cleared", recovered.RestoreFailuresByReason, recovered.RestoreFailedBlocks)
	}
}

func TestEvictionHealthSnapshotUsesApplyStateWithoutCatalogScan(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	assertEvictionApplyCompleted(t, result)
	s.idx = nil

	got, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot should not scan closed catalog: %v", err)
	}
	if got.EvictedBlocks != 1 || got.EvictedBytes != 1024 {
		t.Fatalf("evicted health = %d/%d, want 1/1024", got.EvictedBlocks, got.EvictedBytes)
	}
}

func TestEvictionHealthSnapshotUsesCachedSnapshotForRepeatedProbe(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	stageAlreadyEvictedBlockForEvictionApply(t, s, 1)
	rebuildEvictionHealthForTest(t, s)

	first, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot first: %v", err)
	}
	stageHotConfirmedBlockForEvictionApply(t, s, 2, 2048)
	stageAlreadyEvictedBlockForEvictionApply(t, s, 2)

	second, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot second: %v", err)
	}
	if first.EvictedBlocks != 1 || second.EvictedBlocks != first.EvictedBlocks {
		t.Fatalf("cached evicted blocks = first:%d second:%d, want stable 1", first.EvictedBlocks, second.EvictedBlocks)
	}
}

func TestEvictionHealthRebuildFailsClosedForMalformedMarker(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	if err := os.WriteFile(EvictionMarkerPath(s.blocksDir, 1), []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed eviction marker: %v", err)
	}

	if err := s.rebuildEvictionHealthSnapshot(ctx); err != nil {
		t.Fatalf("rebuildEvictionHealthSnapshot: %v", err)
	}
	got, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if got.UnexpectedLossBlocks != 1 {
		t.Fatalf("unexpected loss blocks = %d, want 1", got.UnexpectedLossBlocks)
	}
}

func TestRefreshRuntimeStateAfterRaftOpenPropagatesHealthRebuildError(t *testing.T) {
	idxDir := t.TempDir()
	idx, err := index.Open(idxDir)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := idx.PutConfirmedUpload(confirmedUploadForEvictionApply(1, 1024)); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}
	writeMalformedConfirmedUploadForEvictionApply(t, idxDir, 1)
	idx, err = index.Open(idxDir)
	if err != nil {
		t.Fatalf("reopen index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	s := &Shard{
		blocksDir:      t.TempDir(),
		shardID:        evictionApplyTestShardID,
		idx:            idx,
		uploadPressure: newUploadPressureCoordinator(),
	}
	s.uploads = newUploadController(s, UploadConfig{}, s.shardID, nil, nil, s.uploadPressure)

	err = s.refreshRuntimeStateAfterRaftOpen()
	if err == nil || !strings.Contains(err.Error(), "rebuild eviction health") {
		t.Fatalf("refreshRuntimeStateAfterRaftOpen error = %v, want rebuild eviction health error", err)
	}
}

func TestApplyEvictionPlanDoesNotRecordHealthForUnconfirmedSelectedBlock(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	plan := storeEvictionApplyPlan(t, s)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	if result.Status != eviction.ApplyStatusNoEffect || len(result.Blocks) != 1 {
		t.Fatalf("result = %+v, want one skipped no-effect Block", result)
	}
	if result.Blocks[0].Reason != eviction.SkipReasonLocalStateNotHot {
		t.Fatalf("skip reason = %q, want local_state_not_hot", result.Blocks[0].Reason)
	}

	got, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if got.MetadataLossBlocks != 0 || got.UnexpectedLossBlocks != 0 {
		t.Fatalf("health loss counts = metadata:%d unexpected:%d, want 0/0", got.MetadataLossBlocks, got.UnexpectedLossBlocks)
	}
}

func TestApplyEvictionPlanDoesNotRecordHealthForForeignShardSelectedBlock(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected[0].ShardID = evictionApplyTestShardID + 1
	storeEvictionPlanForTest(s, plan)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	if result.Status != eviction.ApplyStatusNoEffect || len(result.Blocks) != 1 {
		t.Fatalf("result = %+v, want one skipped no-effect Block", result)
	}
	if result.Blocks[0].Reason != eviction.SkipReasonShardFilter {
		t.Fatalf("skip reason = %q, want shard_filter", result.Blocks[0].Reason)
	}

	got, err := s.EvictionHealthSnapshot(ctx)
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if got.MetadataLossBlocks != 0 || got.UnexpectedLossBlocks != 0 {
		t.Fatalf("health loss counts = metadata:%d unexpected:%d, want 0/0", got.MetadataLossBlocks, got.UnexpectedLossBlocks)
	}
}

func writeMalformedConfirmedUploadForEvictionApply(t *testing.T, idxDir string, blockID uint64) {
	t.Helper()

	db, err := pebble.Open(idxDir, &pebble.Options{})
	if err != nil {
		t.Fatalf("open pebble: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Set(confirmedUploadKeyForEvictionApply(blockID), []byte("{"), pebble.Sync); err != nil {
		t.Fatalf("write malformed confirmed upload: %v", err)
	}
}

func confirmedUploadKeyForEvictionApply(blockID uint64) []byte {
	const prefix = "\x00confirmed-upload\x00"
	key := make([]byte, len(prefix)+8)
	copy(key, prefix)
	binary.BigEndian.PutUint64(key[len(prefix):], blockID)
	return key
}

func rebuildEvictionHealthForTest(t *testing.T, s *Shard) {
	t.Helper()
	if err := s.rebuildEvictionHealthSnapshot(context.Background()); err != nil {
		t.Fatalf("rebuildEvictionHealthSnapshot: %v", err)
	}
}

func TestApplyEvictionPlanFailsBlockWhenConfirmationDrifts(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	plan.Selected[0].SizeBytes = 2048
	storeEvictionPlanForTest(s, plan)

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

func TestApplyEvictionPlanContinuesPastBlockFailure(t *testing.T) {
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
	storeEvictionPlanForTest(s, plan)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}

	if result.Status != eviction.ApplyStatusFailed || result.FailedBlocks != 1 || result.EvictedBlocks != 1 {
		t.Fatalf("result = %+v, want one failure and one eviction", result)
	}
	if len(result.Blocks) != 2 {
		t.Fatalf("blocks = %+v, want failed Block 1 then evicted Block 2", result.Blocks)
	}
	assertEvictionApplyBlock(t, result.Blocks[0], 1, eviction.ApplyBlockStatusFailed)
	assertEvictionApplyBlock(t, result.Blocks[1], 2, eviction.ApplyBlockStatusEvicted)
	assertBlockEvictedForApply(t, s, 2)
	if hasEvictionApplyResultForTest(s, plan.PlanID) {
		t.Fatal("result with retryable failures should not be cached")
	}
}

func TestApplyEvictionPlanRetriesFailedBlocksOnReapply(t *testing.T) {
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
	storeEvictionPlanForTest(s, plan)

	result, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan: %v", err)
	}
	if result.Status != eviction.ApplyStatusFailed || result.EvictedBlocks != 1 || result.FailedBlocks != 1 {
		t.Fatalf("result = %+v, want one eviction then one failure", result)
	}
	if hasEvictionApplyResultForTest(s, plan.PlanID) {
		t.Fatal("result with retryable failures should not be cached")
	}

	retry, err := s.ApplyEvictionPlan(ctx, eviction.ApplyRequest{PlanID: plan.PlanID})
	if err != nil {
		t.Fatalf("ApplyEvictionPlan retry: %v", err)
	}
	if len(retry.Blocks) != 2 {
		t.Fatalf("retry blocks = %+v, want evicted Block skipped and failed Block retried", retry.Blocks)
	}
	assertEvictionApplyBlock(t, retry.Blocks[0], 1, eviction.ApplyBlockStatusSkipped)
	if retry.Blocks[0].Reason != eviction.SkipReasonLocalStateNotHot {
		t.Fatalf("retry blocks = %+v, want evicted Block skipped as local_state_not_hot", retry.Blocks)
	}
	assertEvictionApplyBlock(t, retry.Blocks[1], 2, eviction.ApplyBlockStatusFailed)
}

func assertEvictionApplyBlock(t *testing.T, got eviction.ApplyBlock, blockID uint64, status string) {
	t.Helper()
	if got.BlockID != blockID || got.Status != status {
		t.Fatalf("block = %+v, want Block %d with status %s", got, blockID, status)
	}
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
	if hasEvictionApplyResultForTest(s, plan.PlanID) {
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
	raft := &evictionApplyRaftStub{
		becomeLeaderBeforeMutation: true,
		beforeStableLeadership:     requireShardMutexUnlockedForRaftFence(t, s),
	}
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

func requireShardMutexUnlockedForRaftFence(t *testing.T, s *Shard) func() {
	t.Helper()

	return func() {
		t.Helper()
		if !s.mu.TryLock() {
			t.Fatal("Shard mutex was held before the Raft leadership fence")
		}
		s.mu.Unlock()
	}
}

func TestApplyEvictionPlanUsesDefaultMarkerReason(t *testing.T) {
	ctx := context.Background()
	s := shardForEvictionApplyTest(t, true)
	stageHotConfirmedBlockForEvictionApply(t, s, 1, 1024)
	plan := storeEvictionApplyPlan(t, s)
	plan.Reason = ""
	storeEvictionPlanForTest(s, plan)

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
		evictionCampaigns:    eviction.NewCampaigns(),
		evictionHealthBlocks: make(map[uint64]evictionHealthBlockContribution),
	}
}

type recordingEvictionApplyBackend struct {
	delegate backend.Backend
	heads    int
	gets     int
	lists    int
}

func (b *recordingEvictionApplyBackend) PutObject(ctx context.Context, key string, body io.Reader, size int64, opts backend.PutOpts) (backend.PutResult, error) {
	return b.delegate.PutObject(ctx, key, body, size, opts)
}

func (b *recordingEvictionApplyBackend) HeadObject(ctx context.Context, key string) (backend.ObjectMeta, error) {
	b.heads++
	return b.delegate.HeadObject(ctx, key)
}

func (b *recordingEvictionApplyBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	b.gets++
	return b.delegate.GetObject(ctx, key, opts)
}

func (b *recordingEvictionApplyBackend) DeleteObject(ctx context.Context, key string) error {
	return b.delegate.DeleteObject(ctx, key)
}

func (b *recordingEvictionApplyBackend) ListObjects(ctx context.Context, prefix string, opts backend.ListOpts) (backend.ObjectIterator, error) {
	b.lists++
	return b.delegate.ListObjects(ctx, prefix, opts)
}

type recordingEvictionMetrics struct {
	applySkipCounts map[string]int
}

func (m *recordingEvictionMetrics) RecordPlan(uint64, eviction.Plan) {}

func (m *recordingEvictionMetrics) RecordApply(_ uint64, _, _ string, _ time.Duration, skipCounts map[string]int) {
	m.applySkipCounts = make(map[string]int, len(skipCounts))
	for reason, count := range skipCounts {
		m.applySkipCounts[reason] = count
	}
}

func (m *recordingEvictionMetrics) RecordRestore(uint64, string, string, string, time.Duration) {}

func (m *recordingEvictionMetrics) SetHealth(uint64, eviction.HealthSnapshot) {}

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
	storeEvictionPlanForTest(s, plan)
	return plan
}

func storeEvictionPlanForTest(s *Shard, plan eviction.Plan) {
	s.evictionCampaigns.StorePlan(plan)
}

func finishEvictionApplyForTest(s *Shard, planID string, result eviction.ApplyResult) {
	s.evictionCampaigns.FinishApply(planID, result, true)
}

func beginEvictionApplyForTest(t *testing.T, s *Shard, planID string) {
	t.Helper()

	if _, err := s.evictionCampaigns.BeginApply(planID, s.evictionMember(), time.Now().UTC()); err != nil {
		t.Fatalf("BeginApply test setup: %v", err)
	}
}

func hasEvictionApplyResultForTest(s *Shard, planID string) bool {
	_, ok := s.evictionCampaigns.ApplyResult(planID)
	return ok
}

func storeEvictionApplyPlanForConfirmed(
	t *testing.T,
	s *Shard,
	confirmed index.ConfirmedUpload,
	reason string,
) eviction.Plan {
	t.Helper()

	return storeEvictionApplyPlanForConfirmedUploads(t, s, reason, 1, confirmed)
}

func storeEvictionApplyPlanForConfirmedUploads(
	t *testing.T,
	s *Shard,
	reason string,
	maxValidateSamples int,
	uploads ...index.ConfirmedUpload,
) eviction.Plan {
	t.Helper()

	plan := storeEvictionApplyPlan(t, s)
	plan.Reason = reason
	plan.Config.MaxValidateSamples = maxValidateSamples
	plan.Selected = make([]eviction.PlanBlock, 0, len(uploads))
	for _, upload := range uploads {
		plan.Selected = append(plan.Selected, evictionApplyPlanBlockForConfirmed(upload))
	}
	storeEvictionPlanForTest(s, plan)
	return plan
}

func confirmedUploadForAssertion(t *testing.T, s *Shard, blockID uint64) index.ConfirmedUpload {
	t.Helper()

	confirmed, err := s.idx.GetConfirmedUpload(blockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	return confirmed
}

func evictionApplyPlanBlockForConfirmed(confirmed index.ConfirmedUpload) eviction.PlanBlock {
	return eviction.PlanBlock{
		BlockID:    confirmed.BlockID,
		ShardID:    confirmed.ShardID,
		SizeBytes:  confirmed.BlockObject.SizeBytes,
		BackendKey: confirmed.BlockObject.Key,
		LocalState: string(LocalBlockStateHot),
	}
}

func stageReadableHotConfirmedBlockForEvictionApply(
	ctx context.Context,
	t *testing.T,
	s *Shard,
	backendStore backend.Backend,
	blockID uint64,
	content []byte,
) index.ConfirmedUpload {
	t.Helper()

	if err := os.MkdirAll(s.blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	written := writeReadableBlockForEvictionApply(t, s, blockID, content)

	confirmed := confirmedUploadForEvictionApply(blockID, statFileSize(t, written.blockPath))
	confirmed.BlockObject = putEvictionApplyBackendObject(ctx, t, backendStore, confirmed.BlockObject.Key, written.blockPath)
	confirmed.IndexObject = putEvictionApplyBackendObject(ctx, t, backendStore, confirmed.IndexObject.Key, written.indexPath)
	if err := s.idx.PutConfirmedUpload(confirmed); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}
	if err := s.idx.Put(written.txID, blockID, 1, true); err != nil {
		t.Fatalf("put projection entry: %v", err)
	}
	if err := writeConfirmedUploadAuthority(s.blocksDir, confirmed); err != nil {
		t.Fatalf("writeConfirmedUploadAuthority: %v", err)
	}
	return confirmed
}

type readableEvictionApplyBlock struct {
	blockPath string
	indexPath string
	txID      string
}

func writeReadableBlockForEvictionApply(t *testing.T, s *Shard, blockID uint64, content []byte) readableEvictionApplyBlock {
	t.Helper()

	blkPath := block.FilePath(s.blocksDir, blockID)
	idxPath := block.IdxFilePath(s.blocksDir, blockID)
	bw, err := block.NewWriter(blkPath, s.shardID, blockID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	iw, err := block.NewIndexWriter(idxPath)
	if err != nil {
		t.Fatalf("NewIndexWriter: %v", err)
	}

	txID := evictionApplyTxID(blockID)
	docName := fmt.Sprintf("doc-%d.bin", blockID)
	appendResult, err := bw.AppendDocument(txID, docName, "application/octet-stream", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AppendDocument: %v", err)
	}
	if err := iw.Append(block.IndexEntry{
		TransactionID: txID,
		DocName:       docName,
		ContentType:   "application/octet-stream",
		CreatedAt:     time.Now().UTC(),
		FirstFrameOff: appendResult.FirstFrameOffset,
		FrameCount:    appendResult.FrameCount,
		TotalBytes:    appendResult.Size,
		SHA256:        appendResult.SHA256,
	}); err != nil {
		t.Fatalf("append Block index: %v", err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("close Block writer: %v", err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("close Block index writer: %v", err)
	}
	return readableEvictionApplyBlock{
		blockPath: blkPath,
		indexPath: idxPath,
		txID:      txID,
	}
}

func evictionApplyTxID(blockID uint64) string {
	return fmt.Sprintf("tx-validate-%d", blockID)
}

func statFileSize(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func readEvictionApplyFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // test path is generated from temp dir and Block ID
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func putEvictionApplyBackendObject(
	ctx context.Context,
	t *testing.T,
	backendStore backend.Backend,
	key string,
	path string,
) index.BackendObjectMetadata {
	t.Helper()

	file, err := os.Open(path) //nolint:gosec // test path is generated from temp dir and block ID
	if err != nil {
		t.Fatalf("open backend source %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()

	size := statFileSize(t, path)
	result, err := backendStore.PutObject(ctx, key, file, size, backend.PutOpts{})
	if err != nil {
		t.Fatalf("PutObject %s: %v", key, err)
	}
	return index.BackendObjectMetadata{
		Key:             key,
		SizeBytes:       result.Size,
		ValidationToken: result.ETag,
	}
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

func assertBlockRestoredByValidationForApply(t *testing.T, s *Shard, blockID uint64) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("Block should be restored by validation: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, blockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
	restore, err := ReadRestoreMarker(s.blocksDir, blockID)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != RestoreSourceBackend || restore.Reason != RestoreReasonValidation {
		t.Fatalf("restore marker = %+v, want backend/validation", restore)
	}
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != LocalBlockStateHot || !lifecycle.ServingAllowed {
		t.Fatalf("lifecycle = %+v, want hot serving-allowed", lifecycle)
	}
}

type evictionApplyRaftStub struct {
	leader                     bool
	becomeLeaderBeforeMutation bool
	beforeStableLeadership     func()
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
	if r.beforeStableLeadership != nil {
		r.beforeStableLeadership()
	}
	if r.becomeLeaderBeforeMutation {
		r.leader = true
	}
	return fn()
}

func (r *evictionApplyRaftStub) Stop() {}

type cancelOnGetBackend struct {
	backend.Backend
	cancel context.CancelFunc
}

func (b cancelOnGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	b.cancel()
	return b.Backend.GetObject(ctx, key, opts)
}

type blockingGetBackend struct {
	backend.Backend
	cancel context.CancelFunc
}

func (b blockingGetBackend) GetObject(ctx context.Context, _ string, _ backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	b.cancel()
	<-ctx.Done()
	return nil, backend.ObjectMeta{}, ctx.Err()
}
