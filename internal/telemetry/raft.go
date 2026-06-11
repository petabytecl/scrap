package telemetry

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type RaftStateProvider interface {
	IsLeader() bool
	LeaderID() uint64
	AppliedIndex() uint64
	CommitIndex() uint64
}

type RaftMetrics struct {
	registration metric.Registration
}

func NewRaftMetrics(meter metric.Meter, provider RaftStateProvider, attrs ...attribute.KeyValue) (*RaftMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	if provider == nil {
		return nil, errors.New("raft state provider is required")
	}
	attrs = append([]attribute.KeyValue(nil), attrs...)

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

	commitIndex, err := meter.Int64ObservableGauge("scrap.raft.commit_index",
		metric.WithDescription("Highest committed Raft log index; apply lag = commit_index - applied_index."),
	)
	if err != nil {
		return nil, fmt.Errorf("create raft commit_index gauge: %w", err)
	}

	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			leader := int64(0)
			if provider.IsLeader() {
				leader = 1
			}
			opts := observeOptions(attrs)
			o.ObserveInt64(isLeader, leader, opts...)
			o.ObserveInt64(leaderID, clampUint64(provider.LeaderID()), opts...)
			o.ObserveInt64(appliedIndex, clampUint64(provider.AppliedIndex()), opts...)
			o.ObserveInt64(commitIndex, clampUint64(provider.CommitIndex()), opts...)
			return nil
		},
		isLeader, leaderID, appliedIndex, commitIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("register raft callback: %w", err)
	}

	return &RaftMetrics{
		registration: reg,
	}, nil
}

func clampUint64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
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
