package eviction

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCampaignsStoresPlanAndReportsPendingStatus(t *testing.T) {
	campaigns := NewCampaigns()
	plan := campaignPlanForTest("plan-1")

	campaigns.StorePlan(plan)

	stored, ok := campaigns.Plan(plan.PlanID)
	if !ok || stored.PlanID != plan.PlanID {
		t.Fatalf("stored plan = %+v, %v; want plan-1", stored, ok)
	}
	status, err := campaigns.Status(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.GeneratedAtUs))
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != PlanStatusPending || status.Plan == nil || status.Plan.PlanID != plan.PlanID {
		t.Fatalf("status = %+v, want pending plan", status)
	}
}

func TestCampaignsBeginFinishAndReturnCachedApplyResult(t *testing.T) {
	campaigns := NewCampaigns()
	plan := campaignPlanForTest("plan-1")
	campaigns.StorePlan(plan)

	start, err := campaigns.BeginApply(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.GeneratedAtUs))
	if err != nil {
		t.Fatalf("BeginApply: %v", err)
	}
	if start.Cached {
		t.Fatal("first BeginApply returned cached result")
	}
	if start.Plan.PlanID != plan.PlanID || !campaigns.HasRunningApply() {
		t.Fatalf("start = %+v running=%v, want running plan", start, campaigns.HasRunningApply())
	}

	result := ApplyResult{PlanID: plan.PlanID, Status: ApplyStatusCompleted, CompletedAtUs: plan.GeneratedAtUs + 1}
	campaigns.FinishApply(plan.PlanID, result, true)
	if campaigns.HasRunningApply() {
		t.Fatal("running apply not cleared after FinishApply")
	}

	cached, err := campaigns.BeginApply(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.GeneratedAtUs))
	if err != nil {
		t.Fatalf("BeginApply cached: %v", err)
	}
	if !cached.Cached || cached.Result.CompletedAtUs != result.CompletedAtUs {
		t.Fatalf("cached start = %+v, want completed result", cached)
	}
}

func TestCampaignsDoNotCacheNoEffectResult(t *testing.T) {
	campaigns := NewCampaigns()
	plan := campaignPlanForTest("plan-1")
	campaigns.StorePlan(plan)

	if _, err := campaigns.BeginApply(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.GeneratedAtUs)); err != nil {
		t.Fatalf("BeginApply: %v", err)
	}
	campaigns.FinishApply(plan.PlanID, ApplyResult{PlanID: plan.PlanID, Status: ApplyStatusNoEffect}, true)

	retry, err := campaigns.BeginApply(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.GeneratedAtUs))
	if err != nil {
		t.Fatalf("BeginApply retry: %v", err)
	}
	if retry.Cached {
		t.Fatalf("retry = %+v, want a new apply after no_effect", retry)
	}
}

func TestCampaignsRejectInvalidPlanState(t *testing.T) {
	now := time.Unix(1700, 0)

	tests := []struct {
		name   string
		setup  func(*Campaigns) string
		member Member
		now    time.Time
		want   error
	}{
		{
			name: "missing",
			setup: func(_ *Campaigns) string {
				return "missing"
			},
			member: campaignMemberForTest(),
			now:    now,
			want:   ErrPlanNotFound,
		},
		{
			name: "expired",
			setup: func(c *Campaigns) string {
				plan := campaignPlanForTest("expired")
				plan.ExpiresAtUs = now.Add(-time.Second).UnixMicro()
				c.StorePlan(plan)
				c.FinishApply(plan.PlanID, ApplyResult{PlanID: plan.PlanID, Status: ApplyStatusCompleted}, true)
				return plan.PlanID
			},
			member: campaignMemberForTest(),
			now:    now,
			want:   ErrPlanExpired,
		},
		{
			name: "stale member",
			setup: func(c *Campaigns) string {
				plan := campaignPlanForTest("stale")
				c.StorePlan(plan)
				return plan.PlanID
			},
			member: Member{Hostname: "scrapd-2", ID: "other-member"},
			now:    now,
			want:   ErrPlanStale,
		},
		{
			name: "already running",
			setup: func(c *Campaigns) string {
				plan := campaignPlanForTest("running")
				c.StorePlan(plan)
				if _, err := c.BeginApply(plan.PlanID, campaignMemberForTest(), now); err != nil {
					t.Fatalf("BeginApply setup: %v", err)
				}
				return plan.PlanID
			},
			member: campaignMemberForTest(),
			now:    now,
			want:   ErrApplyInProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			campaigns := NewCampaigns()
			planID := tt.setup(campaigns)

			_, err := campaigns.BeginApply(planID, tt.member, tt.now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("BeginApply error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCampaignsKeepExpiredRunningApplyUntilFinish(t *testing.T) {
	now := time.Unix(1700, 0)
	campaigns := NewCampaigns()
	plan := campaignPlanForTest("plan-running-expired")
	campaigns.StorePlan(plan)
	if _, err := campaigns.BeginApply(plan.PlanID, campaignMemberForTest(), now); err != nil {
		t.Fatalf("BeginApply: %v", err)
	}

	_, err := campaigns.Status(plan.PlanID, campaignMemberForTest(), time.UnixMicro(plan.ExpiresAtUs+1))
	if !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("Status error = %v, want ErrPlanExpired", err)
	}
	if !campaigns.HasRunningApply() {
		t.Fatal("expired status lookup cleared running apply before FinishApply")
	}

	campaigns.FinishApply(plan.PlanID, ApplyResult{PlanID: plan.PlanID, Status: ApplyStatusFailed}, false)
	if campaigns.HasRunningApply() {
		t.Fatal("FinishApply did not clear expired running apply")
	}
}

func TestApplyBlocksCountsResultsAndStopsAtFailure(t *testing.T) {
	plan := campaignPlanForTest("plan-1")
	plan.Selected = append(plan.Selected, PlanBlock{BlockID: 2, ShardID: 7, SizeBytes: 2048})
	var observed []ApplyBlock

	result, cacheable := ApplyBlocks(
		contextWithoutCancelForTest(),
		plan,
		time.Unix(1700, 0),
		func(selected PlanBlock) ApplyBlock {
			if selected.BlockID == 1 {
				return ApplyBlock{
					BlockID:    selected.BlockID,
					ShardID:    selected.ShardID,
					SizeBytes:  selected.SizeBytes,
					Status:     ApplyBlockStatusEvicted,
					BytesFreed: selected.SizeBytes,
				}
			}
			return FailedBlock(selected, errors.New("boom"))
		},
		func(block ApplyBlock) {
			observed = append(observed, block)
		},
	)

	if !cacheable {
		t.Fatal("cacheable = false, want true for completed apply attempt")
	}
	if result.Status != ApplyStatusFailed || result.EvictedBlocks != 1 || result.FailedBlocks != 1 || result.BytesFreed != 1024 {
		t.Fatalf("result = %+v, want one evicted Block and one failure", result)
	}
	if len(observed) != 2 || observed[0].Status != ApplyBlockStatusEvicted || observed[1].Status != ApplyBlockStatusFailed {
		t.Fatalf("observed = %+v, want evicted then failed", observed)
	}
}

func contextWithoutCancelForTest() context.Context {
	return context.Background()
}

func campaignPlanForTest(planID string) Plan {
	generatedAt := time.Unix(1700, 0)
	return Plan{
		PlanID:         planID,
		GeneratedAtUs:  generatedAt.UnixMicro(),
		ExpiresAtUs:    generatedAt.Add(time.Minute).UnixMicro(),
		MemberHostname: "scrapd-2",
		MemberID:       "member-b",
		Selected: []PlanBlock{{
			BlockID:   1,
			ShardID:   7,
			SizeBytes: 1024,
		}},
	}
}

func campaignMemberForTest() Member {
	return Member{Hostname: "scrapd-2", ID: "member-b"}
}
