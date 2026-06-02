package eviction

import (
	"fmt"
	"time"
)

type Campaigns struct {
	plans        map[string]Plan
	applyResults map[string]ApplyResult
	applyRunning map[string]struct{}
}

type ApplyStart struct {
	Plan   Plan
	Result ApplyResult
	Cached bool
}

func NewCampaigns() *Campaigns {
	return &Campaigns{
		plans:        make(map[string]Plan),
		applyResults: make(map[string]ApplyResult),
		applyRunning: make(map[string]struct{}),
	}
}

func (c *Campaigns) StorePlan(plan Plan) {
	c.ensureMaps()
	c.pruneExpired(plan.GeneratedAtUs)
	c.plans[plan.PlanID] = clonePlan(plan)
}

func (c *Campaigns) Plan(planID string) (Plan, bool) {
	c.ensureMaps()
	plan, ok := c.plans[planID]
	if !ok {
		return Plan{}, false
	}
	return clonePlan(plan), true
}

func (c *Campaigns) ApplyResult(planID string) (ApplyResult, bool) {
	c.ensureMaps()
	result, ok := c.applyResults[planID]
	if !ok {
		return ApplyResult{}, false
	}
	return cloneApplyResult(result), true
}

func (c *Campaigns) BeginApply(planID string, member Member, now time.Time) (ApplyStart, error) {
	if planID == "" {
		return ApplyStart{}, fmt.Errorf("%w: plan_id is required", ErrInvalidPlanRequest)
	}
	plan, err := c.validPlan(planID, member, now.UnixMicro())
	if err != nil {
		return ApplyStart{}, err
	}
	if result, ok := c.applyResults[planID]; ok {
		return ApplyStart{Result: cloneApplyResult(result), Cached: true}, nil
	}
	if _, ok := c.applyRunning[planID]; ok {
		return ApplyStart{}, ErrApplyInProgress
	}
	c.applyRunning[planID] = struct{}{}
	return ApplyStart{Plan: plan}, nil
}

func (c *Campaigns) FinishApply(planID string, result ApplyResult, cacheable bool) {
	c.ensureMaps()
	delete(c.applyRunning, planID)
	if cacheable && shouldCacheApplyResult(result) {
		c.applyResults[planID] = cloneApplyResult(result)
	}
}

func (c *Campaigns) Status(planID string, member Member, now time.Time) (PlanStatus, error) {
	if planID == "" {
		return PlanStatus{}, fmt.Errorf("%w: plan_id is required", ErrInvalidPlanRequest)
	}
	plan, err := c.validPlan(planID, member, now.UnixMicro())
	if err != nil {
		return PlanStatus{}, err
	}
	if result, ok := c.applyResults[planID]; ok {
		result := cloneApplyResult(result)
		return PlanStatus{
			PlanID:      planID,
			Status:      result.Status,
			Plan:        newPlanPtr(plan),
			ApplyResult: &result,
		}, nil
	}
	status := PlanStatusPending
	if _, ok := c.applyRunning[planID]; ok {
		status = PlanStatusRunning
	}
	return PlanStatus{
		PlanID: planID,
		Status: status,
		Plan:   newPlanPtr(plan),
	}, nil
}

func (c *Campaigns) HasRunningApply() bool {
	c.ensureMaps()
	return len(c.applyRunning) > 0
}

func (c *Campaigns) validPlan(planID string, member Member, nowUs int64) (Plan, error) {
	c.ensureMaps()
	plan, ok := c.plans[planID]
	if !ok {
		return Plan{}, ErrPlanNotFound
	}
	if plan.ExpiresAtUs <= nowUs {
		delete(c.plans, planID)
		delete(c.applyResults, planID)
		return Plan{}, ErrPlanExpired
	}
	if plan.MemberHostname != member.Hostname || plan.MemberID != member.ID {
		return Plan{}, ErrPlanStale
	}
	return clonePlan(plan), nil
}

func (c *Campaigns) pruneExpired(nowUs int64) {
	for planID, plan := range c.plans {
		if plan.ExpiresAtUs <= nowUs {
			delete(c.plans, planID)
			delete(c.applyResults, planID)
		}
	}
}

func (c *Campaigns) ensureMaps() {
	if c.plans == nil {
		c.plans = make(map[string]Plan)
	}
	if c.applyResults == nil {
		c.applyResults = make(map[string]ApplyResult)
	}
	if c.applyRunning == nil {
		c.applyRunning = make(map[string]struct{})
	}
}

func shouldCacheApplyResult(result ApplyResult) bool {
	return result.Status != "" && result.Status != ApplyStatusNoEffect
}

func newPlanPtr(plan Plan) *Plan {
	plan = clonePlan(plan)
	return &plan
}

func clonePlan(plan Plan) Plan {
	plan.Selected = append([]PlanBlock(nil), plan.Selected...)
	plan.Skipped = append([]PlanBlock(nil), plan.Skipped...)
	if plan.SkipCountsByReason != nil {
		counts := make(map[string]int, len(plan.SkipCountsByReason))
		for reason, count := range plan.SkipCountsByReason {
			counts[reason] = count
		}
		plan.SkipCountsByReason = counts
	}
	return plan
}

func cloneApplyResult(result ApplyResult) ApplyResult {
	result.Blocks = append([]ApplyBlock(nil), result.Blocks...)
	result.Validations = append([]ValidationBlock(nil), result.Validations...)
	return result
}
