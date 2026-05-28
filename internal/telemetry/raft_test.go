package telemetry_test

import (
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/telemetry"
)

type stubRaftState struct {
	isLeader     bool
	leaderID     uint64
	appliedIndex uint64
}

func (s *stubRaftState) IsLeader() bool       { return s.isLeader }
func (s *stubRaftState) LeaderID() uint64     { return s.leaderID }
func (s *stubRaftState) AppliedIndex() uint64 { return s.appliedIndex }

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
	}

	rm, err := telemetry.NewRaftMetrics(provider.Meter("test"), state)
	if err != nil {
		t.Fatalf("new raft metrics: %v", err)
	}
	defer func() { _ = rm.Unregister() }()

	metrics := collectMetrics(t, reader)

	isLeader := findMetric(metrics, "scrap.raft.is_leader")
	if isLeader == nil {
		t.Fatal("scrap.raft.is_leader not found")
	}
	gauge, ok := isLeader.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("expected Gauge[int64], got %T", isLeader.Data)
	}
	if len(gauge.DataPoints) == 0 || gauge.DataPoints[0].Value != 1 {
		t.Fatalf("is_leader: want 1, got %v", gauge.DataPoints)
	}

	leaderID := findMetric(metrics, "scrap.raft.leader_id")
	if leaderID == nil {
		t.Fatal("scrap.raft.leader_id not found")
	}

	appliedIndex := findMetric(metrics, "scrap.raft.applied_index")
	if appliedIndex == nil {
		t.Fatal("scrap.raft.applied_index not found")
	}
}

func TestRaftMetrics_NilUnregister(t *testing.T) {
	var rm *telemetry.RaftMetrics
	if err := rm.Unregister(); err != nil {
		t.Fatalf("nil unregister: %v", err)
	}
}
