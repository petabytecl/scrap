package telemetry

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.opentelemetry.io/otel/metric"
)

type RaftStateProvider interface {
	IsLeader() bool
	LeaderID() uint64
	AppliedIndex() uint64
}

type RaftMetrics struct {
	proposals    metric.Int64Counter
	registration metric.Registration
}

func NewRaftMetrics(meter metric.Meter, provider RaftStateProvider) (*RaftMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	if provider == nil {
		return nil, errors.New("raft state provider is required")
	}

	isLeader, err := meter.Int64ObservableGauge("scrap.raft.is_leader",
		metric.WithDescription("Whether this member is the Raft leader (1) or follower (0)."),
	)
	if err != nil {
		return nil, fmt.Errorf("create raft is_leader gauge: %w", err)
	}

	leaderID, err := meter.Int64ObservableGauge("scrap.raft.leader_id",
		metric.WithDescription("Raft ID of the current leader."),
	)
	if err != nil {
		return nil, fmt.Errorf("create raft leader_id gauge: %w", err)
	}

	appliedIndex, err := meter.Int64ObservableGauge("scrap.raft.applied_index",
		metric.WithDescription("Last applied Raft log index."),
	)
	if err != nil {
		return nil, fmt.Errorf("create raft applied_index gauge: %w", err)
	}

	proposals, err := meter.Int64Counter("scrap.raft.proposals",
		metric.WithDescription("Total Raft proposals submitted."),
	)
	if err != nil {
		return nil, fmt.Errorf("create raft proposals counter: %w", err)
	}

	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			leader := int64(0)
			if provider.IsLeader() {
				leader = 1
			}
			o.ObserveInt64(isLeader, leader)
			o.ObserveInt64(leaderID, clampUint64(provider.LeaderID()))
			o.ObserveInt64(appliedIndex, clampUint64(provider.AppliedIndex()))
			return nil
		},
		isLeader, leaderID, appliedIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("register raft callback: %w", err)
	}

	return &RaftMetrics{
		proposals:    proposals,
		registration: reg,
	}, nil
}

func clampUint64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func (r *RaftMetrics) RecordProposal() {
	r.proposals.Add(context.Background(), 1)
}

func (r *RaftMetrics) Unregister() error {
	if r == nil || r.registration == nil {
		return nil
	}
	if err := r.registration.Unregister(); err != nil {
		return fmt.Errorf("unregister raft metrics: %w", err)
	}
	return nil
}
