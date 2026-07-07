package eviction

import (
	"fmt"
	"sort"
	"time"
)

type PlanConfig struct {
	Enabled              bool
	HotResidencyWindow   time.Duration
	PlanTTL              time.Duration
	RecommendedMaxBlocks int
	RecommendedMaxBytes  int64
	MaxValidateSamples   int
}

type PlanInput struct {
	Request    PlanRequest
	Config     PlanConfig
	Member     Member
	Now        time.Time
	PlanID     string
	IsLeader   bool
	Candidates []PlanCandidate
}

type PlanCandidate struct {
	BlockID        uint64
	ShardID        uint64
	SizeBytes      int64
	BackendKey     string
	ConfirmedAtUs  int64
	RestoredAtUs   int64
	LocalState     string
	LocalEvictable bool
	OpenReaders    int
	RepairState    string
}

func BuildPlan(input PlanInput) (Plan, error) {
	if err := validatePlanInput(input); err != nil {
		return Plan{}, err
	}

	reason := input.Request.Reason
	if reason == "" {
		reason = ReasonEvidenceRun
	}
	recommended := Bounds{
		MaxBlocks: input.Config.RecommendedMaxBlocks,
		MaxBytes:  input.Config.RecommendedMaxBytes,
	}
	requested, effective, err := planBounds(input.Request, recommended)
	if err != nil {
		return Plan{}, err
	}

	generatedAtUs := input.Now.UnixMicro()
	plan := Plan{
		PlanID:             input.PlanID,
		GeneratedAtUs:      generatedAtUs,
		ExpiresAtUs:        input.Now.Add(input.Config.PlanTTL).UnixMicro(),
		MemberHostname:     input.Member.Hostname,
		MemberID:           input.Member.ID,
		ShardID:            input.Request.ShardID,
		Reason:             reason,
		Note:               input.Request.Note,
		Config:             planConfigSnapshot(input.Config),
		RecommendedBounds:  recommended,
		RequestedBounds:    requested,
		EffectiveBounds:    effective,
		SkipCountsByReason: make(map[string]int),
	}

	candidates := append([]PlanCandidate(nil), input.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].BlockID < candidates[j].BlockID
	})

	for _, candidate := range candidates {
		block := planBlock(candidate, input.Config)
		plan.CandidateBlocks++
		plan.CandidateBytes += block.SizeBytes

		if reason := planSkipReason(candidate, input.Request, input.IsLeader, generatedAtUs, block.EligibleAtUs); reason != "" {
			appendSkippedBlock(&plan, block, reason)
			continue
		}

		plan.EligibleBlocks++
		plan.EligibleBytes += block.SizeBytes
		if len(plan.Selected) >= effective.MaxBlocks || plan.SelectedBytes+block.SizeBytes > effective.MaxBytes {
			appendSkippedBlock(&plan, block, SkipReasonPlanBounds)
			continue
		}

		plan.Selected = append(plan.Selected, block)
		plan.SelectedBytes += block.SizeBytes
	}
	if len(plan.SkipCountsByReason) == 0 {
		plan.SkipCountsByReason = nil
	}
	return plan, nil
}

func validatePlanInput(input PlanInput) error {
	if err := validatePlanIdentity(input); err != nil {
		return err
	}
	if err := validatePlanRequest(input); err != nil {
		return err
	}
	return validatePlanConfig(input.Config)
}

func validatePlanIdentity(input PlanInput) error {
	if input.PlanID == "" {
		return fmt.Errorf("%w: plan_id is required", ErrInvalidPlanRequest)
	}
	if input.Member.Hostname == "" {
		return fmt.Errorf("%w: local member_hostname is required", ErrInvalidPlanRequest)
	}
	if input.Member.ID == "" {
		return fmt.Errorf("%w: local member_id is required", ErrInvalidPlanRequest)
	}
	if input.Request.MemberHostname == "" {
		return fmt.Errorf("%w: member_hostname is required", ErrInvalidPlanRequest)
	}
	if input.Request.MemberHostname != input.Member.Hostname {
		return fmt.Errorf("%w: got %q want %q", ErrTargetMemberMismatch, input.Request.MemberHostname, input.Member.Hostname)
	}
	return nil
}

func validatePlanRequest(input PlanInput) error {
	if input.Request.Reason != "" && input.Request.Reason != ReasonEvidenceRun {
		return fmt.Errorf("%w: unsupported reason %q", ErrInvalidPlanRequest, input.Request.Reason)
	}
	return nil
}

func validatePlanConfig(cfg PlanConfig) error {
	if cfg.HotResidencyWindow < 0 {
		return fmt.Errorf("%w: hot residency window must be >= 0", ErrInvalidPlanRequest)
	}
	if cfg.PlanTTL <= 0 {
		return fmt.Errorf("%w: plan ttl must be > 0", ErrInvalidPlanRequest)
	}
	if cfg.RecommendedMaxBlocks <= 0 {
		return fmt.Errorf("%w: recommended max_blocks must be > 0", ErrInvalidPlanRequest)
	}
	if cfg.RecommendedMaxBytes <= 0 {
		return fmt.Errorf("%w: recommended max_bytes must be > 0", ErrInvalidPlanRequest)
	}
	if cfg.MaxValidateSamples < 0 {
		return fmt.Errorf("%w: max validate samples must be >= 0", ErrInvalidPlanRequest)
	}
	return nil
}

func planBounds(req PlanRequest, recommended Bounds) (Bounds, Bounds, error) {
	requested := Bounds{}
	effective := recommended
	if req.MaxBlocks != nil {
		if *req.MaxBlocks <= 0 {
			return requested, effective, fmt.Errorf("%w: max_blocks must be > 0", ErrInvalidPlanRequest)
		}
		if *req.MaxBlocks > recommended.MaxBlocks {
			return requested, effective, fmt.Errorf("%w: max_blocks %d exceeds %d", ErrPlanCapExceedsCeiling, *req.MaxBlocks, recommended.MaxBlocks)
		}
		requested.MaxBlocks = *req.MaxBlocks
		effective.MaxBlocks = *req.MaxBlocks
	}
	if req.MaxBytes != nil {
		if *req.MaxBytes <= 0 {
			return requested, effective, fmt.Errorf("%w: max_bytes must be > 0", ErrInvalidPlanRequest)
		}
		if *req.MaxBytes > recommended.MaxBytes {
			return requested, effective, fmt.Errorf("%w: max_bytes %d exceeds %d", ErrPlanCapExceedsCeiling, *req.MaxBytes, recommended.MaxBytes)
		}
		requested.MaxBytes = *req.MaxBytes
		effective.MaxBytes = *req.MaxBytes
	}
	return requested, effective, nil
}

func planConfigSnapshot(cfg PlanConfig) ConfigSnapshot {
	return ConfigSnapshot{
		Enabled:                   cfg.Enabled,
		HotResidencyWindowSeconds: int64(cfg.HotResidencyWindow / time.Second),
		PlanTTLSeconds:            int64(cfg.PlanTTL / time.Second),
		RecommendedMaxBlocks:      cfg.RecommendedMaxBlocks,
		RecommendedMaxBytes:       cfg.RecommendedMaxBytes,
		MaxValidateSamples:        cfg.MaxValidateSamples,
	}
}

func planBlock(candidate PlanCandidate, cfg PlanConfig) PlanBlock {
	hotResidencyBasisUs := candidate.ConfirmedAtUs
	if candidate.RestoredAtUs > hotResidencyBasisUs {
		hotResidencyBasisUs = candidate.RestoredAtUs
	}
	eligibleAtUs := hotResidencyBasisUs + cfg.HotResidencyWindow.Microseconds()
	repairState := candidate.RepairState
	if repairState == "" {
		repairState = RepairStateIdle
	}
	return PlanBlock{
		BlockID:                   candidate.BlockID,
		ShardID:                   candidate.ShardID,
		SizeBytes:                 candidate.SizeBytes,
		BackendKey:                candidate.BackendKey,
		ConfirmedAtUs:             candidate.ConfirmedAtUs,
		RestoredAtUs:              candidate.RestoredAtUs,
		EligibleAtUs:              eligibleAtUs,
		HotResidencyWindowSeconds: int64(cfg.HotResidencyWindow / time.Second),
		LocalState:                candidate.LocalState,
		OpenReaders:               candidate.OpenReaders,
		RepairState:               repairState,
	}
}

func planSkipReason(candidate PlanCandidate, req PlanRequest, isLeader bool, nowUs, eligibleAtUs int64) string {
	if req.ShardID != nil && candidate.ShardID != *req.ShardID {
		return SkipReasonShardFilter
	}
	if !candidate.LocalEvictable {
		return SkipReasonLocalStateNotHot
	}
	// Never evict the last durable copy: a candidate without a Backend object key
	// has no offsite copy to restore from. The shard's ConfirmedUploads source
	// enforces this today, but the plan layer must not depend on the caller — a
	// future candidate source could feed unconfirmed Blocks straight to
	// UnlinkBlockData. (ConfirmedAtUs is a hot-residency timing input, not a
	// durability signal — restored Blocks carry a durable copy with an unrelated
	// ConfirmedAtUs.)
	if candidate.BackendKey == "" {
		return SkipReasonNoDurableCopy
	}
	if isLeader {
		return SkipReasonLeaderHotCopyRequired
	}
	if nowUs < eligibleAtUs {
		return SkipReasonHotResidencyWindow
	}
	return ""
}

func appendSkippedBlock(plan *Plan, block PlanBlock, reason string) {
	block.Reason = reason
	plan.Skipped = append(plan.Skipped, block)
	plan.SkipCountsByReason[reason]++
}
