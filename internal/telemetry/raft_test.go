package telemetry_test

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/petabytecl/scrap/internal/telemetry"
)

type stubRaftState struct {
	isLeader     bool
	leaderID     uint64
	appliedIndex uint64
	commitIndex  uint64
}

func (s *stubRaftState) IsLeader() bool       { return s.isLeader }
func (s *stubRaftState) LeaderID() uint64     { return s.leaderID }
func (s *stubRaftState) AppliedIndex() uint64 { return s.appliedIndex }
func (s *stubRaftState) CommitIndex() uint64  { return s.commitIndex }

func TestRaftMetrics_NilMeter(t *testing.T) {
	_, err := telemetry.NewRaftMetrics(nil, &stubRaftState{})
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestRaftMetrics_NilProvider(t *testing.T) {
	provider, _ := newTestMeter(t)
	_, err := telemetry.NewRaftMetrics(provider.Meter("test"), nil)
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestRaftMetrics_ObservesState(t *testing.T) {
	provider, reader := newTestMeter(t)
	state := &stubRaftState{
		isLeader:     true,
		leaderID:     3,
		appliedIndex: 42,
		commitIndex:  50,
	}

	rm, err := telemetry.NewRaftMetrics(provider.Meter("test"), state)
	if err != nil {
		t.Fatalf("new raft metrics: %v", err)
	}
	defer func() { _ = rm.Unregister() }()

	metrics := collectMetrics(t, reader)

	assertInt64Gauge(t, metrics, "scrap.raft.is_leader", 1)
	assertInt64Gauge(t, metrics, "scrap.raft.leader_id", 3)
	assertInt64Gauge(t, metrics, "scrap.raft.applied_index", 42)
	assertInt64Gauge(t, metrics, "scrap.raft.commit_index", 50)
}

func TestRaftMetrics_ObservesShardAttribute(t *testing.T) {
	provider, reader := newTestMeter(t)
	state := &stubRaftState{leaderID: 3, appliedIndex: 42, commitIndex: 50}

	rm, err := telemetry.NewRaftMetrics(provider.Meter("test"), state, attribute.Int64("scrap.shard_id", 7))
	if err != nil {
		t.Fatalf("new raft metrics: %v", err)
	}
	defer func() { _ = rm.Unregister() }()

	metrics := collectMetrics(t, reader)

	assertInt64GaugeAttribute(t, metrics, "scrap.raft.commit_index", "scrap.shard_id", int64(7))
}

func TestRaftMetrics_NilUnregister(t *testing.T) {
	var rm *telemetry.RaftMetrics
	if err := rm.Unregister(); err != nil {
		t.Fatalf("nil unregister: %v", err)
	}
}
