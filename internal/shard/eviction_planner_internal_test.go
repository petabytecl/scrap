package shard

import (
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
)

func TestBuildEvictionPlanSelectsOldestEligibleFollowerBlocksWithinBounds(t *testing.T) {
	cfg := EvictionConfig{
		HotResidencyWindow:   time.Hour,
		PlanTTL:              10 * time.Minute,
		RecommendedMaxBlocks: 2,
		RecommendedMaxBytes:  500,
		MaxValidateSamples:   1,
	}
	now := time.UnixMicro(10_000_000_000)

	plan, err := buildEvictionPlan(evictionPlanInput{
		Request: eviction.PlanRequest{MemberHostname: "scrapd-2"},
		Config:  cfg,
		Member:  evictionMember{Hostname: "scrapd-2", ID: "member-b"},
		Now:     now,
		PlanID:  "plan-abc",
		Candidates: []evictionCandidate{
			candidateForPlanTest(3, 100, now.Add(-2*time.Hour)),
			candidateForPlanTest(1, 200, now.Add(-3*time.Hour)),
			candidateForPlanTest(2, 100, now.Add(-30*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("buildEvictionPlan: %v", err)
	}

	assertPlanSelectionForTest(t, plan)
}

func assertPlanSelectionForTest(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if len(plan.Selected) != 2 {
		t.Fatalf("selected = %+v, want 2 Blocks", plan.Selected)
	}
	if plan.Selected[0].BlockID != 1 || plan.Selected[1].BlockID != 3 {
		t.Fatalf("selected order = %+v, want Block 1 then 3", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].BlockID != 2 || plan.Skipped[0].Reason != eviction.SkipReasonHotResidencyWindow {
		t.Fatalf("skipped = %+v, want recent Block skipped by hot window", plan.Skipped)
	}
	if plan.SelectedBytes != 300 || plan.EligibleBlocks != 2 || plan.EligibleBytes != 300 {
		t.Fatalf("plan totals = selected_bytes:%d eligible_blocks:%d eligible_bytes:%d", plan.SelectedBytes, plan.EligibleBlocks, plan.EligibleBytes)
	}
}

func TestBuildEvictionPlanRejectsExpandedCaps(t *testing.T) {
	cfg := EvictionConfig{
		HotResidencyWindow:   time.Hour,
		PlanTTL:              10 * time.Minute,
		RecommendedMaxBlocks: 2,
		RecommendedMaxBytes:  500,
	}
	tooMany := 3

	_, err := buildEvictionPlan(evictionPlanInput{
		Request: eviction.PlanRequest{MemberHostname: "scrapd-2", MaxBlocks: &tooMany},
		Config:  cfg,
		Member:  evictionMember{Hostname: "scrapd-2", ID: "member-b"},
		Now:     time.Now(),
		PlanID:  "plan-abc",
	})
	if err == nil {
		t.Fatal("buildEvictionPlan succeeded with expanded max_blocks")
	}
}

func TestBuildEvictionPlanUsesRestoreTimeForHotResidency(t *testing.T) {
	cfg := EvictionConfig{
		HotResidencyWindow:   time.Hour,
		PlanTTL:              10 * time.Minute,
		RecommendedMaxBlocks: 2,
		RecommendedMaxBytes:  500,
	}
	confirmedAt := time.UnixMicro(10_000_000_000).Add(-24 * time.Hour)
	restoredAt := time.UnixMicro(10_000_000_000)
	now := restoredAt.Add(30 * time.Minute)
	candidate := candidateForPlanTest(1, 200, confirmedAt)
	candidate.Lifecycle.RestoreMarker = &RestoreMarker{
		Version:      LifecycleMarkerVersion,
		BlockID:      1,
		RestoredAtUs: restoredAt.UnixMicro(),
		Source:       RestoreSourceBackend,
		Reason:       RestoreReasonRead,
	}

	plan, err := buildEvictionPlan(evictionPlanInput{
		Request:    eviction.PlanRequest{MemberHostname: "scrapd-2"},
		Config:     cfg,
		Member:     evictionMember{Hostname: "scrapd-2", ID: "member-b"},
		Now:        now,
		PlanID:     "plan-abc",
		Candidates: []evictionCandidate{candidate},
	})
	if err != nil {
		t.Fatalf("buildEvictionPlan: %v", err)
	}

	if len(plan.Selected) != 0 {
		t.Fatalf("selected = %+v, want none before restored hot window expires", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != eviction.SkipReasonHotResidencyWindow {
		t.Fatalf("skipped = %+v, want hot residency skip", plan.Skipped)
	}
	if plan.Skipped[0].RestoredAtUs != restoredAt.UnixMicro() {
		t.Fatalf("restored_at_us = %d, want %d", plan.Skipped[0].RestoredAtUs, restoredAt.UnixMicro())
	}
	if plan.Skipped[0].EligibleAtUs != restoredAt.Add(time.Hour).UnixMicro() {
		t.Fatalf("eligible_at_us = %d, want %d", plan.Skipped[0].EligibleAtUs, restoredAt.Add(time.Hour).UnixMicro())
	}
}

func candidateForPlanTest(blockID uint64, sizeBytes int64, confirmedAt time.Time) evictionCandidate {
	return evictionCandidate{
		Upload: index.ConfirmedUpload{
			BlockID:         blockID,
			ShardID:         7,
			ConfirmedAtUs:   confirmedAt.UnixMicro(),
			SealedSizeBytes: sizeBytes,
			BlockObject: index.BackendObjectMetadata{
				Key:             "cell/shards/7/block.blk",
				SizeBytes:       sizeBytes,
				ValidationToken: "block-validation",
			},
			IndexObject: index.BackendObjectMetadata{
				Key:             "cell/shards/7/block.idx",
				SizeBytes:       1024,
				ValidationToken: "index-validation",
			},
		},
		Lifecycle: LocalBlockLifecycle{
			BlockID:        blockID,
			State:          LocalBlockStateHot,
			ServingAllowed: true,
			HealthDegraded: false,
			EvictionMarker: nil,
			RestoreMarker:  nil,
		},
	}
}
