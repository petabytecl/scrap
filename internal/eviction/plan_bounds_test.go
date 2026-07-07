package eviction

// Coverage for the plan-bounds guards (#471c): the caps that stop an operator
// from requesting an over-large eviction beyond the configured ceiling, plus
// the accept path that narrows the effective bounds.

import (
	"errors"
	"testing"
	"time"
)

func TestPlanBoundsRejectionsAndAcceptance(t *testing.T) {
	zero := 0
	negBytes := int64(-1)
	overBlocks := 3          // ceiling is 2
	overBytes := int64(1000) // ceiling is 500
	zeroBytes := int64(0)
	okBlocks := 1
	okBytes := int64(200)

	tests := []struct {
		name    string
		req     PlanRequest
		wantErr error
	}{
		{name: "max_blocks zero", req: PlanRequest{MaxBlocks: &zero}, wantErr: ErrInvalidPlanRequest},
		{name: "max_blocks over ceiling", req: PlanRequest{MaxBlocks: &overBlocks}, wantErr: ErrPlanCapExceedsCeiling},
		{name: "max_bytes zero", req: PlanRequest{MaxBytes: &zeroBytes}, wantErr: ErrInvalidPlanRequest},
		{name: "max_bytes negative", req: PlanRequest{MaxBytes: &negBytes}, wantErr: ErrInvalidPlanRequest},
		{name: "max_bytes over ceiling", req: PlanRequest{MaxBytes: &overBytes}, wantErr: ErrPlanCapExceedsCeiling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.req
			req.MemberHostname = "scrapd-2"
			_, err := BuildPlan(PlanInput{
				Request: req,
				Config:  planConfigForTest(),
				Member:  Member{Hostname: "scrapd-2", ID: "member-b"},
				Now:     time.Now(),
				PlanID:  "plan-bounds",
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("BuildPlan error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	// Within-ceiling caps are accepted and narrow the effective bounds.
	plan, err := BuildPlan(PlanInput{
		Request: PlanRequest{MemberHostname: "scrapd-2", MaxBlocks: &okBlocks, MaxBytes: &okBytes},
		Config:  planConfigForTest(),
		Member:  Member{Hostname: "scrapd-2", ID: "member-b"},
		Now:     time.Now(),
		PlanID:  "plan-bounds-ok",
	})
	if err != nil {
		t.Fatalf("BuildPlan within bounds: %v", err)
	}
	if plan.EffectiveBounds.MaxBlocks != okBlocks || plan.EffectiveBounds.MaxBytes != okBytes {
		t.Fatalf("effective bounds = %+v, want blocks=%d bytes=%d", plan.EffectiveBounds, okBlocks, okBytes)
	}
	if plan.RequestedBounds.MaxBlocks != okBlocks || plan.RequestedBounds.MaxBytes != okBytes {
		t.Fatalf("requested bounds = %+v, want blocks=%d bytes=%d", plan.RequestedBounds, okBlocks, okBytes)
	}
}
