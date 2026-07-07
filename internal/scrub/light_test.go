package scrub_test

import (
	"context"
	"errors"
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
	results  map[string]scrub.Result
	errAddrs map[string]error
	err      error
	calls    atomic.Int32
}

func (m *mockConsistencyChecker) CheckConsistency(_ context.Context, addr, scrubID string) (scrub.Result, error) {
	m.calls.Add(1)
	if err, ok := m.errAddrs[addr]; ok {
		return scrub.Result{}, err
	}
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
	ok        int
	mismatch  int
	errCount  int
	quorumTie int
	duration  float64
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
	case "quorum_tie":
		m.recorded.quorumTie++
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

	ls := scrub.NewLight(scrub.LightConfig{
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

type transientConsistencyChecker struct {
	result   scrub.Result
	failures atomic.Int32
	calls    atomic.Int32
}

func (c *transientConsistencyChecker) CheckConsistency(_ context.Context, _, scrubID string) (scrub.Result, error) {
	c.calls.Add(1)
	if c.failures.Add(-1) >= 0 {
		return scrub.Result{}, scrub.ErrConsistencyResultNotReady
	}
	result := c.result
	result.ScrubID = scrubID
	return result, nil
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

	ls := scrub.NewLight(scrub.LightConfig{
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

func TestLightScrubber_RetriesTransientPeerResultNotReady(t *testing.T) {
	hash := [32]byte{0xde, 0xad}
	checker := &transientConsistencyChecker{
		result:   scrub.Result{AppliedIndex: 10, SHA256: hash},
		failures: atomic.Int32{},
	}
	checker.failures.Store(1)
	metrics := &mockMetrics{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: hash}},
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091"},
		PeerResultTimeout:  100 * time.Millisecond,
		PeerResultPoll:     time.Millisecond,
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if checker.calls.Load() < 2 {
		t.Fatalf("expected retry after transient not-ready, got %d calls", checker.calls.Load())
	}
	if metrics.recorded.ok != 1 || metrics.recorded.errCount != 0 {
		t.Fatalf("metrics = %+v, want one ok and no errors", metrics.recorded)
	}
}

func TestLightScrubber_ReportsPeerResultTimeout(t *testing.T) {
	checker := &mockConsistencyChecker{err: scrub.ErrConsistencyResultNotReady}
	metrics := &mockMetrics{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           &mockProposer{result: scrub.Result{SHA256: [32]byte{1}}},
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091"},
		PeerResultTimeout:  5 * time.Millisecond,
		PeerResultPoll:     time.Millisecond,
	})

	err := ls.RunOnce(context.Background())
	if !errors.Is(err, scrub.ErrConsistencyResultNotReady) {
		t.Fatalf("RunOnce error = %v, want %v", err, scrub.ErrConsistencyResultNotReady)
	}

	if metrics.recorded.errCount != 1 {
		t.Fatalf("expected 1 error run, got %d", metrics.recorded.errCount)
	}
}

func TestLightScrubber_AllPeersUnreachableIsNotOK(t *testing.T) {
	// Every peer errors with a non-NotReady failure (e.g. unreachable), so no
	// peer result is ever compared against the leader hash.
	checker := &mockConsistencyChecker{err: errors.New("dial peer: connection refused")}
	metrics := &mockMetrics{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           &mockProposer{result: scrub.Result{SHA256: [32]byte{1}}},
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
		PeerResultTimeout:  5 * time.Millisecond,
		PeerResultPoll:     time.Millisecond,
	})

	err := ls.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce reported success when every peer check failed, want a degraded error")
	}
	if metrics.recorded.ok != 0 {
		t.Fatalf("expected no ok run, got %d", metrics.recorded.ok)
	}
	if metrics.recorded.errCount != 1 {
		t.Fatalf("expected 1 error run, got %d", metrics.recorded.errCount)
	}
}

func TestLightScrubber_SkipsWhenNotLeader(t *testing.T) {
	metrics := &mockMetrics{}

	ls := scrub.NewLight(scrub.LightConfig{
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

	ls := scrub.NewLight(scrub.LightConfig{
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

	ls := scrub.NewLight(scrub.LightConfig{
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
		"peer-3:9091": {AppliedIndex: 10, SHA256: leaderHash},
		"peer-4:9091": {AppliedIndex: 10, SHA256: leaderHash},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091", "peer-3:9091", "peer-4:9091"},
	})

	err := ls.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(rebuilder.requested) != 2 {
		t.Fatalf("expected 2 rebuild requests, got %d: %v", len(rebuilder.requested), rebuilder.requested)
	}
}

func TestLightScrubber_NoRebuildOnMatch(t *testing.T) {
	hash := [32]byte{0xaa}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: hash}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: hash},
	}}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
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

func TestLightScrubber_ZeroLeaderHashIsErrorNotDivergence(t *testing.T) {
	// A zero hash means the voter failed to compute one; comparing it would
	// mark every healthy peer divergent and trigger projection rebuilds.
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {SHA256: [32]byte{2}},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           &mockProposer{result: scrub.Result{}},
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		PeerAddrs:          []string{"peer-1:9091"},
		PeerResultTimeout:  5 * time.Millisecond,
		PeerResultPoll:     time.Millisecond,
	})

	if err := ls.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce with zero leader hash succeeded, want error")
	}
	if metrics.recorded.errCount != 1 {
		t.Fatalf("expected 1 error run, got %d", metrics.recorded.errCount)
	}
	if len(rebuilder.requested) != 0 {
		t.Fatalf("expected no rebuild requests off a zero hash, got %v", rebuilder.requested)
	}
}

type mockSelfRebuilder struct {
	calls int
	err   error
}

func (m *mockSelfRebuilder) RequestSelfRebuild(context.Context, string) error {
	m.calls++
	return m.err
}

// Regression for #470: when the leader's projection hash is the minority, the
// quorum must rebuild the leader itself instead of rebuilding every healthy
// peer forever.
func TestLightScrubber_CorruptLeaderRebuildsSelfByMajority(t *testing.T) {
	peerHash := [32]byte{0xbb}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: [32]byte{0xaa}}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: peerHash},
		"peer-2:9091": {AppliedIndex: 10, SHA256: peerHash},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}
	selfRebuilder := &mockSelfRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		SelfRebuilder:      selfRebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	if err := ls.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if selfRebuilder.calls != 1 {
		t.Fatalf("self rebuilds = %d, want 1 (leader is the minority)", selfRebuilder.calls)
	}
	if len(rebuilder.requested) != 0 {
		t.Fatalf("peer rebuilds = %v, want none (peers hold the majority)", rebuilder.requested)
	}
	if metrics.recorded.mismatch != 1 {
		t.Fatalf("mismatch runs = %d, want 1", metrics.recorded.mismatch)
	}
}

// Regression for #470: a hash tie (no strict majority) freezes — no rebuilds
// in either direction, a distinct run result, and an error for the operator.
func TestLightScrubber_QuorumTieFreezesWithoutRebuilds(t *testing.T) {
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: [32]byte{0xaa}}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: [32]byte{0xbb}},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}
	selfRebuilder := &mockSelfRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		SelfRebuilder:      selfRebuilder,
		PeerAddrs:          []string{"peer-1:9091"},
	})

	if err := ls.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce succeeded, want quorum-tie error")
	}
	if len(rebuilder.requested) != 0 || selfRebuilder.calls != 0 {
		t.Fatalf("rebuilds on tie: peers=%v self=%d, want none", rebuilder.requested, selfRebuilder.calls)
	}
	if metrics.recorded.quorumTie != 1 {
		t.Fatalf("quorum_tie runs = %d, want 1", metrics.recorded.quorumTie)
	}
}

// Three-way disagreement has no majority either: freeze rather than trust the
// leader's copy by default.
func TestLightScrubber_ThreeWayDisagreementFreezes(t *testing.T) {
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: [32]byte{0xaa}}}
	checker := &mockConsistencyChecker{results: map[string]scrub.Result{
		"peer-1:9091": {AppliedIndex: 10, SHA256: [32]byte{0xbb}},
		"peer-2:9091": {AppliedIndex: 10, SHA256: [32]byte{0xcc}},
	}}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}
	selfRebuilder := &mockSelfRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		SelfRebuilder:      selfRebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091"},
	})

	if err := ls.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce succeeded, want quorum-tie error")
	}
	if len(rebuilder.requested) != 0 || selfRebuilder.calls != 0 {
		t.Fatalf("rebuilds on three-way disagreement: peers=%v self=%d, want none", rebuilder.requested, selfRebuilder.calls)
	}
	if metrics.recorded.quorumTie != 1 {
		t.Fatalf("quorum_tie runs = %d, want 1", metrics.recorded.quorumTie)
	}
}

// Review finding on #489: the quorum is sized by the CONFIGURED voter set.
// Two reachable peers in a 5-voter Cell (two peers unreachable) must not
// outvote the leader — unreachable voters are unknowns, not abstentions.
func TestLightScrubber_UnreachablePeersCannotFormMajority(t *testing.T) {
	peerHash := [32]byte{0xbb}
	proposer := &mockProposer{result: scrub.Result{AppliedIndex: 10, SHA256: [32]byte{0xaa}}}
	checker := &mockConsistencyChecker{
		results: map[string]scrub.Result{
			"peer-1:9091": {AppliedIndex: 10, SHA256: peerHash},
			"peer-2:9091": {AppliedIndex: 10, SHA256: peerHash},
		},
		errAddrs: map[string]error{
			"peer-3:9091": errors.New("unreachable"),
			"peer-4:9091": errors.New("unreachable"),
		},
	}
	metrics := &mockMetrics{}
	rebuilder := &mockRebuilder{}
	selfRebuilder := &mockSelfRebuilder{}

	ls := scrub.NewLight(scrub.LightConfig{
		Proposer:           proposer,
		ConsistencyChecker: checker,
		LeaderChecker:      &mockLeaderChecker{leader: true},
		Metrics:            metrics,
		Rebuilder:          rebuilder,
		SelfRebuilder:      selfRebuilder,
		PeerAddrs:          []string{"peer-1:9091", "peer-2:9091", "peer-3:9091", "peer-4:9091"},
	})

	if err := ls.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce succeeded, want no-majority error with two voters unreachable")
	}
	if selfRebuilder.calls != 0 || len(rebuilder.requested) != 0 {
		t.Fatalf("rebuilds: self=%d peers=%v, want none without a configured-voter majority", selfRebuilder.calls, rebuilder.requested)
	}
	if metrics.recorded.quorumTie != 1 {
		t.Fatalf("quorum_tie runs = %d, want 1", metrics.recorded.quorumTie)
	}
}
