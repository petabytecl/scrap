package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/ulid"
)

const evictionRepairStateIdle = "idle"

type evictionMember struct {
	Hostname string
	ID       string
}

type evictionPlanInput struct {
	Request    eviction.PlanRequest
	Config     EvictionConfig
	Member     evictionMember
	Now        time.Time
	PlanID     string
	IsLeader   bool
	Candidates []evictionCandidate
}

type evictionCandidate struct {
	Upload    index.ConfirmedUpload
	Lifecycle LocalBlockLifecycle
}

func (s *Shard) CreateEvictionPlan(ctx context.Context, req eviction.PlanRequest) (eviction.Plan, error) {
	select {
	case <-ctx.Done():
		return eviction.Plan{}, ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	candidates, err := s.evictionCandidatesLocked()
	if err != nil {
		return eviction.Plan{}, err
	}

	plan, err := buildEvictionPlan(evictionPlanInput{
		Request:    req,
		Config:     s.eviction,
		Member:     evictionMember{Hostname: s.memberHostname, ID: s.memberID},
		Now:        time.Now().UTC(),
		PlanID:     ulid.New().String(),
		IsLeader:   s.raft.IsLeader(),
		Candidates: candidates,
	})
	if err != nil {
		return eviction.Plan{}, err
	}

	s.pruneExpiredEvictionPlansLocked(plan.GeneratedAtUs)
	s.evictionPlans[plan.PlanID] = plan
	return plan, nil
}

func (s *Shard) EvictionPlanForTest(planID string) (eviction.Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	plan, ok := s.evictionPlans[planID]
	return plan, ok
}

func (s *Shard) evictionCandidatesLocked() ([]evictionCandidate, error) {
	iter, err := s.idx.ConfirmedUploads()
	if err != nil {
		return nil, err
	}

	var candidates []evictionCandidate
	for {
		upload, err := iter.Next()
		if err == nil {
			lifecycle, err := ClassifyLocalBlock(s.blocksDir, upload.BlockID)
			if err != nil {
				return nil, fmt.Errorf("shard: classify local Block %d: %w", upload.BlockID, err)
			}
			candidates = append(candidates, evictionCandidate{Upload: upload, Lifecycle: lifecycle})
			continue
		}
		if errors.Is(err, io.EOF) {
			return candidates, nil
		}
		return nil, err
	}
}

func (s *Shard) pruneExpiredEvictionPlansLocked(nowUs int64) {
	for planID, plan := range s.evictionPlans {
		if plan.ExpiresAtUs <= nowUs {
			delete(s.evictionPlans, planID)
		}
	}
}

func buildEvictionPlan(input evictionPlanInput) (eviction.Plan, error) {
	if err := validateEvictionPlanInput(input); err != nil {
		return eviction.Plan{}, err
	}

	cfg := input.Config.withDefaults()
	reason := input.Request.Reason
	if reason == "" {
		reason = eviction.ReasonEvidenceRun
	}
	recommended := eviction.Bounds{MaxBlocks: cfg.RecommendedMaxBlocks, MaxBytes: cfg.RecommendedMaxBytes}
	requested, effective, err := evictionPlanBounds(input.Request, recommended)
	if err != nil {
		return eviction.Plan{}, err
	}

	generatedAtUs := input.Now.UnixMicro()
	plan := eviction.Plan{
		PlanID:             input.PlanID,
		GeneratedAtUs:      generatedAtUs,
		ExpiresAtUs:        input.Now.Add(cfg.PlanTTL).UnixMicro(),
		MemberHostname:     input.Member.Hostname,
		MemberID:           input.Member.ID,
		ShardID:            input.Request.ShardID,
		Reason:             reason,
		Note:               input.Request.Note,
		Config:             evictionConfigSnapshot(cfg),
		RecommendedBounds:  recommended,
		RequestedBounds:    requested,
		EffectiveBounds:    effective,
		SkipCountsByReason: make(map[string]int),
	}

	candidates := append([]evictionCandidate(nil), input.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Upload.BlockID < candidates[j].Upload.BlockID
	})

	for _, candidate := range candidates {
		block := planBlock(candidate, cfg)
		plan.CandidateBlocks++
		plan.CandidateBytes += block.SizeBytes

		if reason := evictionSkipReason(candidate, input.Request, input.IsLeader, generatedAtUs, block.EligibleAtUs); reason != "" {
			appendSkippedBlock(&plan, block, reason)
			continue
		}

		plan.EligibleBlocks++
		plan.EligibleBytes += block.SizeBytes
		if len(plan.Selected) >= effective.MaxBlocks || plan.SelectedBytes+block.SizeBytes > effective.MaxBytes {
			appendSkippedBlock(&plan, block, eviction.SkipReasonPlanBounds)
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

func validateEvictionPlanInput(input evictionPlanInput) error {
	if input.PlanID == "" {
		return fmt.Errorf("%w: plan_id is required", eviction.ErrInvalidPlanRequest)
	}
	if input.Member.Hostname == "" {
		return fmt.Errorf("%w: local member_hostname is required", eviction.ErrInvalidPlanRequest)
	}
	if input.Member.ID == "" {
		return fmt.Errorf("%w: local member_id is required", eviction.ErrInvalidPlanRequest)
	}
	if input.Request.MemberHostname == "" {
		return fmt.Errorf("%w: member_hostname is required", eviction.ErrInvalidPlanRequest)
	}
	if input.Request.MemberHostname != input.Member.Hostname {
		return fmt.Errorf("%w: got %q want %q", eviction.ErrTargetMemberMismatch, input.Request.MemberHostname, input.Member.Hostname)
	}
	if input.Request.Reason != "" && input.Request.Reason != eviction.ReasonEvidenceRun {
		return fmt.Errorf("%w: unsupported reason %q", eviction.ErrInvalidPlanRequest, input.Request.Reason)
	}
	cfg := input.Config.withDefaults()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%w: %w", eviction.ErrInvalidPlanRequest, err)
	}
	return nil
}

func evictionPlanBounds(req eviction.PlanRequest, recommended eviction.Bounds) (eviction.Bounds, eviction.Bounds, error) {
	requested := eviction.Bounds{}
	effective := recommended
	if req.MaxBlocks != nil {
		if *req.MaxBlocks <= 0 {
			return requested, effective, fmt.Errorf("%w: max_blocks must be > 0", eviction.ErrInvalidPlanRequest)
		}
		if *req.MaxBlocks > recommended.MaxBlocks {
			return requested, effective, fmt.Errorf("%w: max_blocks %d exceeds %d", eviction.ErrPlanCapExceedsCeiling, *req.MaxBlocks, recommended.MaxBlocks)
		}
		requested.MaxBlocks = *req.MaxBlocks
		effective.MaxBlocks = *req.MaxBlocks
	}
	if req.MaxBytes != nil {
		if *req.MaxBytes <= 0 {
			return requested, effective, fmt.Errorf("%w: max_bytes must be > 0", eviction.ErrInvalidPlanRequest)
		}
		if *req.MaxBytes > recommended.MaxBytes {
			return requested, effective, fmt.Errorf("%w: max_bytes %d exceeds %d", eviction.ErrPlanCapExceedsCeiling, *req.MaxBytes, recommended.MaxBytes)
		}
		requested.MaxBytes = *req.MaxBytes
		effective.MaxBytes = *req.MaxBytes
	}
	return requested, effective, nil
}

func evictionConfigSnapshot(cfg EvictionConfig) eviction.ConfigSnapshot {
	return eviction.ConfigSnapshot{
		Enabled:                   cfg.Enabled,
		HotResidencyWindowSeconds: int64(cfg.HotResidencyWindow / time.Second),
		PlanTTLSeconds:            int64(cfg.PlanTTL / time.Second),
		RecommendedMaxBlocks:      cfg.RecommendedMaxBlocks,
		RecommendedMaxBytes:       cfg.RecommendedMaxBytes,
		MaxValidateSamples:        cfg.MaxValidateSamples,
	}
}

func planBlock(candidate evictionCandidate, cfg EvictionConfig) eviction.PlanBlock {
	restoredAtUs := int64(0)
	hotResidencyBasisUs := candidate.Upload.ConfirmedAtUs
	if candidate.Lifecycle.RestoreMarker != nil {
		restoredAtUs = candidate.Lifecycle.RestoreMarker.RestoredAtUs
		hotResidencyBasisUs = max(hotResidencyBasisUs, restoredAtUs)
	}
	eligibleAtUs := hotResidencyBasisUs + cfg.HotResidencyWindow.Microseconds()
	return eviction.PlanBlock{
		BlockID:                   candidate.Upload.BlockID,
		ShardID:                   candidate.Upload.ShardID,
		SizeBytes:                 candidate.Upload.SealedSizeBytes,
		BackendKey:                candidate.Upload.BlockObject.Key,
		ConfirmedAtUs:             candidate.Upload.ConfirmedAtUs,
		RestoredAtUs:              restoredAtUs,
		EligibleAtUs:              eligibleAtUs,
		HotResidencyWindowSeconds: int64(cfg.HotResidencyWindow / time.Second),
		LocalState:                string(candidate.Lifecycle.State),
		OpenReaders:               0,
		RepairState:               evictionRepairStateIdle,
	}
}

func evictionSkipReason(candidate evictionCandidate, req eviction.PlanRequest, isLeader bool, nowUs, eligibleAtUs int64) string {
	if req.ShardID != nil && candidate.Upload.ShardID != *req.ShardID {
		return eviction.SkipReasonShardFilter
	}
	if candidate.Lifecycle.State != LocalBlockStateHot {
		return eviction.SkipReasonLocalStateNotHot
	}
	if isLeader {
		return eviction.SkipReasonLeaderHotCopyRequired
	}
	if nowUs < eligibleAtUs {
		return eviction.SkipReasonHotResidencyWindow
	}
	return ""
}

func appendSkippedBlock(plan *eviction.Plan, block eviction.PlanBlock, reason string) {
	block.Reason = reason
	plan.Skipped = append(plan.Skipped, block)
	plan.SkipCountsByReason[reason]++
}
