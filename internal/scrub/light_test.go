package scrub_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
)

type mockProposer struct {
	result scrub.Result
	err    error
}

func (m *mockProposer) ProposeConsistencyCheck(_ context.Context, scrubID string) (scrub.Result, error) {
	if m.err != nil {
		return scrub.Result{}, m.err
	}
	r := m.result
	r.ScrubID = scrubID
	return r, nil
}

type mockConsistencyChecker struct {
	results map[string]scrub.Result
	err     error
}

func (m *mockConsistencyChecker) CheckConsistency(_ context.Context, addr, scrubID string) (scrub.Result, error) {
	if m.err != nil {
		return scrub.Result{}, m.err
	}
	r, ok := m.results[addr]
	if !ok {
		return scrub.Result{}, m.err
	}
	r.ScrubID = scrubID
	return r, nil
}

type mockLeaderChecker struct {
	leader bool
}

func (m *mockLeaderChecker) IsLeader() bool { return m.leader }

type scrubMetrics struct {
	ok       int
	mismatch int
	errCount int
	duration float64
}

type mockMetrics struct {
	recorded scrubMetrics
}

func (m *mockMetrics) RecordRun(result string, durationSec float64) {
	switch result {
	case "ok":
		m.recorded.ok++
	case "mismatch":
		m.recorded.mismatch++
	case "error":
		m.recorded.errCount++
	}
	m.recorded.duration = durationSec
}

func TestLightScrubber_AllMatch(t *testing.T) {
	hash := [32]byte{0xde, 0xad, 0xbe, 0xef}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: hash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: hash},
		"peer-2:9091": {AppliedIndex: 10, SHA256: hash},
	}}
	metrics := &mockMetrics{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if metrics.recorded.ok != 1 {
		t.Fatalf("expected 1 ok run, got %d", metrics.recorded.ok)
	}
	if metrics.recorded.mismatch != 0 {
		t.Fatalf("expected 0 mismatch, got %d", metrics.recorded.mismatch)
	}
}

func TestLightScrubber_Mismatch(t *testing.T) {
	leaderHash := [32]byte{0xaa, 0xbb}
	peerHash := [32]byte{0xcc, 0xdd}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: leaderHash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: leaderHash},
		"peer-2:9091": {AppliedIndex: 10, SHA256: peerHash},
	}}
	metrics := &mockMetrics{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if metrics.recorded.mismatch != 1 {
		t.Fatalf("expected 1 mismatch, got %d", metrics.recorded.mismatch)
	}
	if metrics.recorded.ok != 0 {
		t.Fatalf("expected 0 ok, got %d", metrics.recorded.ok)
	}
}

func TestLightScrubber_SkipsWhenNotLeader(t *testing.T) {
	metrics := &mockMetrics{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           &mockProposer{},
		ConsistencyChecker: &mockConsistencyChecker{},
		LeaderChecker:      &mockLeaderChecker{leader: false},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if metrics.recorded.ok+metrics.recorded.mismatch+metrics.recorded.errCount != 0 {
		t.Fatal("expected no metrics emitted when not leader")
	}
}

func TestLightScrubber_StartStop(t *testing.T) {
	hash := [32]byte{0x11}
	var runCount atomic.Int32
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 1, SHA256: hash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 1, SHA256: hash},
	}}
	metrics := &mockMetrics{}

	countingProposer := &countingProposer{inner: proposer, count: &runCount}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           countingProposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091"},
		Interval:           50 * time.Millisecond,
		Jitter:             0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	ls.Start(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runCount.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	ls.Stop()

	if runCount.Load() < 2 {
		t.Fatalf("expected at least 2 runs, got %d", runCount.Load())
	}
}

type countingProposer struct {
	inner *mockProposer
	count *atomic.Int32
}

func (c *countingProposer) ProposeConsistencyCheck(ctx context.Context, scrubID string) (scrub.Result, error) {
	c.count.Add(1)
	return c.inner.ProposeConsistencyCheck(ctx, scrubID)
}

type mockRebuilder struct {
	requested []string
	err       error
}

func (m *mockRebuilder) RequestRebuild(_ context.Context, addr, _ string) error {
	if m.err != nil {
		return m.err
	}
	m.requested = append(m.requested, addr)
	return nil
}

func TestLightScrubber_MismatchTriggersRebuild(t *testing.T) {
	leaderHash := [32]byte{0xaa, 0xbb}
	peerHash := [32]byte{0xcc, 0xdd}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: leaderHash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: leaderHash},
		"peer-2:9091": {AppliedIndex: 10, SHA256: peerHash},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(rebuilder.requested) != 1 {
		t.Fatalf("expected 1 rebuild request, got %d: %v", len(rebuilder.requested), rebuilder.requested)
	}
	if rebuilder.requested[0] != "peer-2:9091" {
		t.Fatalf("expected rebuild for peer-2:9091, got %s", rebuilder.requested[0])
	}
}

func TestLightScrubber_MultipleMismatchesRebuildAll(t *testing.T) {
	leaderHash := [32]byte{0xaa}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: leaderHash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: [32]byte{0xbb}},
		"peer-2:9091": {AppliedIndex: 10, SHA256: [32]byte{0xcc}},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(rebuilder.requested) != 2 {
		t.Fatalf("expected 2 rebuild requests, got %d", len(rebuilder.requested))
	}
}

func TestLightScrubber_NoRebuildOnMatch(t *testing.T) {
	hash := [32]byte{0xaa}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: hash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: hash},
	}}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLightScrubber(scrub.LightScrubberConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            &mockMetrics{},
		Rebuilder:          rebuilder,
		PeerAddrs:          []string{"peer-1:9091"},
	})

	if err := ls.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(rebuilder.requested) != 0 {
		t.Fatalf("expected 0 rebuild requests, got %d", len(rebuilder.requested))
	}
}
