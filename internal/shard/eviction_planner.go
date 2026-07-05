package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
	"github.com/petabytecl/scrap/internal/ulid"
)

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

	plan, err := eviction.BuildPlan(eviction.PlanInput{
		Request:    req,
		Config:     evictionPlanConfig(s.eviction.withDefaults()),
		Member:     s.evictionMember(),
		Now:        time.Now().UTC(),
		PlanID:     ulid.New().String(),
		IsLeader:   s.raft != nil && s.raft.IsLeader(),
		Candidates: candidates,
	})
	if err != nil {
		return eviction.Plan{}, err
	}

	s.evictionCampaigns.StorePlan(plan)
	s.evictionMetricRecorder().RecordPlan(s.shardID, plan)
	return plan, nil
}

func (s *Shard) EvictionPlanForTest(planID string) (eviction.Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.evictionCampaigns.Plan(planID)
}

func (s *Shard) evictionCandidatesLocked() ([]eviction.PlanCandidate, error) {
	pendingUploads, err := collectPendingUploads(s.idx)
	if err != nil {
		return nil, err
	}
	pendingByBlockID := make(map[uint64]struct{}, len(pendingUploads))
	for _, upload := range pendingUploads {
		pendingByBlockID[upload.BlockID] = struct{}{}
	}

	iter, err := s.idx.ConfirmedUploads()
	if err != nil {
		return nil, err
	}

	var candidates []eviction.PlanCandidate
	for {
		upload, err := iter.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return candidates, nil
			}
			return nil, err
		}
		if _, pending := pendingByBlockID[upload.BlockID]; pending {
			continue
		}
		candidate, eligible, err := s.evictionCandidateLocked(upload)
		if err != nil {
			return nil, err
		}
		if eligible {
			candidates = append(candidates, candidate)
		}
	}
}

func (s *Shard) evictionCandidateLocked(upload index.ConfirmedUpload) (eviction.PlanCandidate, bool, error) {
	lifecycle, err := localblock.Classify(s.blocksDir, upload.BlockID)
	if err != nil {
		return eviction.PlanCandidate{}, false, fmt.Errorf("shard: classify local Block %d: %w", upload.BlockID, err)
	}
	// ADR 0016 eligibility: quarantined Blocks are not eviction candidates.
	// Classify is quarantine-blind, so check explicitly.
	quarantined, err := quarantinedBlockExists(s.blocksDir, upload.BlockID)
	if err != nil {
		return eviction.PlanCandidate{}, false, err
	}
	if quarantined {
		return eviction.PlanCandidate{}, false, nil
	}
	return evictionPlanCandidate(upload, lifecycle), true, nil
}

func evictionPlanCandidate(upload index.ConfirmedUpload, lifecycle localblock.Lifecycle) eviction.PlanCandidate {
	restoredAtUs := int64(0)
	if lifecycle.RestoreMarker != nil {
		restoredAtUs = lifecycle.RestoreMarker.RestoredAtUs
	}
	return eviction.PlanCandidate{
		BlockID:        upload.BlockID,
		ShardID:        upload.ShardID,
		SizeBytes:      upload.SealedSizeBytes,
		BackendKey:     upload.BlockObject.Key,
		ConfirmedAtUs:  upload.ConfirmedAtUs,
		RestoredAtUs:   restoredAtUs,
		LocalState:     string(lifecycle.State),
		LocalEvictable: lifecycle.State == localblock.StateHot,
		OpenReaders:    0,
		RepairState:    eviction.RepairStateIdle,
	}
}

func evictionPlanConfig(cfg EvictionConfig) eviction.PlanConfig {
	return eviction.PlanConfig{
		Enabled:              cfg.Enabled,
		HotResidencyWindow:   cfg.HotResidencyWindow,
		PlanTTL:              cfg.PlanTTL,
		RecommendedMaxBlocks: cfg.RecommendedMaxBlocks,
		RecommendedMaxBytes:  cfg.RecommendedMaxBytes,
		MaxValidateSamples:   cfg.MaxValidateSamples,
	}
}

func (s *Shard) evictionMember() eviction.Member {
	return eviction.Member{Hostname: s.memberHostname, ID: s.memberID}
}
