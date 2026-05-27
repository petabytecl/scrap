package shard_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/shard"
)

type deepScrubProbeMetrics struct {
	mu             sync.Mutex
	framesVerified uint64
	ran            chan struct{}
	once           sync.Once
}

func newDeepScrubProbeMetrics() *deepScrubProbeMetrics {
	return &deepScrubProbeMetrics{ran: make(chan struct{})}
}

func (m *deepScrubProbeMetrics) RecordDeepRun(string, float64) {}

func (m *deepScrubProbeMetrics) RecordFramesVerified(n uint64) {
	m.mu.Lock()
	m.framesVerified += n
	m.mu.Unlock()
	m.once.Do(func() { close(m.ran) })
}

func (m *deepScrubProbeMetrics) RecordCorruption(string)  {}
func (m *deepScrubProbeMetrics) RecordQuarantine()        {}
func (m *deepScrubProbeMetrics) SetBadDiskSuspected(bool) {}
func (m *deepScrubProbeMetrics) RecordPause()             {}
func (m *deepScrubProbeMetrics) SetProgressRatio(float64) {}
func (m *deepScrubProbeMetrics) RecordRepair(string)      {}
func (m *deepScrubProbeMetrics) DecrementQuarantined()    {}

func (m *deepScrubProbeMetrics) verifiedFrames() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.framesVerified
}

func TestShardStartsDeepScrubberForSealedBlocks(t *testing.T) {
	metrics := newDeepScrubProbeMetrics()
	s, err := shard.Open(shard.Config{
		DataDir:       t.TempDir(),
		ShardID:       0,
		RaftID:        1,
		Peers:         map[uint64]string{1: "localhost:9091"},
		BlockSealSize: 64,
		TickInterval:  10 * time.Millisecond,
		Scrub: scrub.ScrubConfig{
			Enabled:           true,
			DeepScrubInterval: 10 * time.Millisecond,
			DeepScrubIORate:   scrub.DefaultDeepScrubIORate,
			PauseLatency:      scrub.DefaultPauseLatency,
			PauseCooldown:     scrub.DefaultPauseCooldown,
			CorruptCap:        scrub.DefaultCorruptCap,
			Jitter:            0,
		},
		DeepScrubMetrics: metrics,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	waitForTestLeader(t, s)

	ctx := context.Background()
	if _, err := s.WriteDocument(ctx, "tx-deep-scrub", "a.xml", "text/xml", "", bytes.NewReader(bytes.Repeat([]byte("a"), 128))); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-deep-scrub", "b.xml", "text/xml", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}

	select {
	case <-metrics.ran:
	case <-time.After(2 * time.Second):
		t.Fatal("deep scrubber did not verify a sealed block")
	}
	if metrics.verifiedFrames() == 0 {
		t.Fatal("expected deep scrub to verify at least one frame")
	}
}

func waitForTestLeader(t *testing.T, s *shard.Shard) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
}
