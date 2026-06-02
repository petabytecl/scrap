package eviction

import (
	"errors"
	"testing"
	"time"
)

func TestBuildPlanSelectsOldestEligibleFollowerBlocksWithinBounds(t *testing.T) {
	now := time.UnixMicro(10_000_000_000)

	plan, err := BuildPlan(PlanInput{
		Request: PlanRequest{MemberHostname: "scrapd-2"},
		Config:  planConfigForTest(),
		Member:  Member{Hostname: "scrapd-2", ID: "member-b"},
		Now:     now,
		PlanID:  "plan-abc",
		Candidates: []PlanCandidate{
			planCandidateForTest(3, 100, now.Add(-2*time.Hour)),
			planCandidateForTest(1, 200, now.Add(-3*time.Hour)),
			planCandidateForTest(2, 100, now.Add(-30*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	assertPlanSelectionForTest(t, plan)
}

func assertPlanSelectionForTest(t *testing.T, plan Plan) {
	t.Helper()

	if len(plan.Selected) != 2 {
		t.Fatalf("selected = %+v, want 2 Blocks", plan.Selected)
	}
	if plan.Selected[0].BlockID != 1 || plan.Selected[1].BlockID != 3 {
		t.Fatalf("selected order = %+v, want Block 1 then 3", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].BlockID != 2 || plan.Skipped[0].Reason != SkipReasonHotResidencyWindow {
		t.Fatalf("skipped = %+v, want recent Block skipped by hot window", plan.Skipped)
	}
	if plan.SelectedBytes != 300 || plan.EligibleBlocks != 2 || plan.EligibleBytes != 300 {
		t.Fatalf("plan totals = selected_bytes:%d eligible_blocks:%d eligible_bytes:%d", plan.SelectedBytes, plan.EligibleBlocks, plan.EligibleBytes)
	}
}

func TestBuildPlanRejectsExpandedCaps(t *testing.T) {
	tooMany := 3

	_, err := BuildPlan(PlanInput{
		Request: PlanRequest{MemberHostname: "scrapd-2", MaxBlocks: &tooMany},
		Config:  planConfigForTest(),
		Member:  Member{Hostname: "scrapd-2", ID: "member-b"},
		Now:     time.Now(),
		PlanID:  "plan-abc",
	})
	if !errors.Is(err, ErrPlanCapExceedsCeiling) {
		t.Fatalf("BuildPlan error = %v, want ErrPlanCapExceedsCeiling", err)
	}
}

func TestBuildPlanUsesRestoreTimeForHotResidency(t *testing.T) {
	confirmedAt := time.UnixMicro(10_000_000_000).Add(-24 * time.Hour)
	restoredAt := time.UnixMicro(10_000_000_000)
	now := restoredAt.Add(30 * time.Minute)
	candidate := planCandidateForTest(1, 200, confirmedAt)
	candidate.RestoredAtUs = restoredAt.UnixMicro()

	plan, err := BuildPlan(PlanInput{
		Request:    PlanRequest{MemberHostname: "scrapd-2"},
		Config:     planConfigForTest(),
		Member:     Member{Hostname: "scrapd-2", ID: "member-b"},
		Now:        now,
		PlanID:     "plan-abc",
		Candidates: []PlanCandidate{candidate},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	if len(plan.Selected) != 0 {
		t.Fatalf("selected = %+v, want none before restored hot window expires", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipReasonHotResidencyWindow {
		t.Fatalf("skipped = %+v, want hot residency skip", plan.Skipped)
	}
	if plan.Skipped[0].RestoredAtUs != restoredAt.UnixMicro() {
		t.Fatalf("restored_at_us = %d, want %d", plan.Skipped[0].RestoredAtUs, restoredAt.UnixMicro())
	}
	if plan.Skipped[0].EligibleAtUs != restoredAt.Add(time.Hour).UnixMicro() {
		t.Fatalf("eligible_at_us = %d, want %d", plan.Skipped[0].EligibleAtUs, restoredAt.Add(time.Hour).UnixMicro())
	}
}

func planConfigForTest() PlanConfig {
	return PlanConfig{
		HotResidencyWindow:   time.Hour,
		PlanTTL:              10 * time.Minute,
		RecommendedMaxBlocks: 2,
		RecommendedMaxBytes:  500,
		MaxValidateSamples:   1,
	}
}

func planCandidateForTest(blockID uint64, sizeBytes int64, confirmedAt time.Time) PlanCandidate {
	return PlanCandidate{
		BlockID:        blockID,
		ShardID:        7,
		SizeBytes:      sizeBytes,
		BackendKey:     "cell/shards/7/block.blk",
		ConfirmedAtUs:  confirmedAt.UnixMicro(),
		LocalState:     "hot",
		LocalEvictable: true,
		RepairState:    RepairStateIdle,
	}
}
