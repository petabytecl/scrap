package scrub_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

type stubBlockLister struct {
	blocks []block.Info
	err    error
}

func (s *stubBlockLister) ListSealedBlocks(openBlockID uint64) ([]block.Info, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []block.Info
	for _, b := range s.blocks {
		if b.BlockID != openBlockID {
			out = append(out, b)
		}
	}
	return out, nil
}

type orderedVerifier struct {
	mu      sync.Mutex
	results []block.VerifyResult
	idx     int
}

func (v *orderedVerifier) VerifyBlock(_, _ string) (block.VerifyResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.idx >= len(v.results) {
		return block.VerifyResult{}, nil
	}
	r := v.results[v.idx]
	v.idx++
	return r, nil
}

type stubQuarantineManager struct {
	quarantined []string
	err         error
}

func (s *stubQuarantineManager) Quarantine(blkPath string) error {
	if s.err != nil {
		return s.err
	}
	s.quarantined = append(s.quarantined, blkPath)
	return nil
}

type stubBlockRepairer struct {
	calls  int
	record func()
}

func (s *stubBlockRepairer) RepairQuarantined(_ context.Context) {
	s.calls++
	if s.record != nil {
		s.record()
	}
}

type recordingVerifier struct {
	calls  int
	record func()
}

func (v *recordingVerifier) VerifyBlock(_, _ string) (block.VerifyResult, error) {
	v.calls++
	if v.record != nil {
		v.record()
	}
	return block.VerifyResult{}, nil
}

type stubScrubBlockClassifier struct {
	states map[uint64]scrub.BlockLocalState
	err    error
}

func (s *stubScrubBlockClassifier) ClassifyScrubBlock(blockID uint64) (scrub.BlockLocalState, error) {
	if s.err != nil {
		return "", s.err
	}
	if state, ok := s.states[blockID]; ok {
		return state, nil
	}
	return scrub.BlockLocalStateHot, nil
}

type controlledPause struct {
	waiting chan struct{}
	resume  chan struct{}
}

func newControlledPause() *controlledPause {
	return &controlledPause{
		waiting: make(chan struct{}),
		resume:  make(chan struct{}),
	}
}

func (p *controlledPause) IsPaused() bool {
	return true
}

func (p *controlledPause) Wait(ctx context.Context) error {
	close(p.waiting)
	select {
	case <-p.resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *controlledPause) Resume() {
	close(p.resume)
}

type deepScrubMetrics struct {
	mu              sync.Mutex
	runsOK          int
	runsError       int
	framesVerified  uint64
	corruptionsCRC  int
	corruptionsSHA  int
	quarantines     int
	badDiskSet      bool
	pauses          int
	progressRatio   float64
	durationSeconds float64
	repairsOK       int
	repairsFailed   int
	decremented     int
	skips           map[string]int
}

func (m *deepScrubMetrics) RecordDeepRun(result string, durationSec float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch result {
	case "ok":
		m.runsOK++
	case "error":
		m.runsError++
	}
	m.durationSeconds = durationSec
}

func (m *deepScrubMetrics) RecordFramesVerified(n uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.framesVerified += n
}

func (m *deepScrubMetrics) RecordCorruption(corruptionType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch corruptionType {
	case "frame_crc":
		m.corruptionsCRC++
	case "doc_sha256":
		m.corruptionsSHA++
	}
}

func (m *deepScrubMetrics) RecordQuarantine() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quarantines++
}

func (m *deepScrubMetrics) SetBadDiskSuspected(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.badDiskSet = v
}

func (m *deepScrubMetrics) RecordPause() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pauses++
}

func (m *deepScrubMetrics) SetProgressRatio(v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progressRatio = v
}

func (m *deepScrubMetrics) RecordRepair(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch result {
	case "ok":
		m.repairsOK++
	case "failed":
		m.repairsFailed++
	}
}

func (m *deepScrubMetrics) DecrementQuarantined() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decremented++
}

func (m *deepScrubMetrics) RecordSkip(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.skips == nil {
		m.skips = make(map[string]int)
	}
	m.skips[reason]++
}

type stubCheckpointStore struct {
	blockID uint64
	set     bool
}

func (s *stubCheckpointStore) GetDeepScrubCheckpoint() (uint64, bool) {
	return s.blockID, s.set
}

func (s *stubCheckpointStore) SetDeepScrubCheckpoint(blockID uint64) {
	s.blockID = blockID
	s.set = true
}

func (s *stubCheckpointStore) ClearDeepScrubCheckpoint() {
	s.blockID = 0
	s.set = false
}

func TestDeepScrubber_KeepsBadDiskGaugeWhenListingFails(t *testing.T) {
	metrics := &deepScrubMetrics{}
	// A prior cycle latched the gauge on over-cap corruption.
	metrics.badDiskSet = true

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       &stubBlockLister{err: errors.New("read dir: input/output error")},
		BlockVerifier:     &orderedVerifier{},
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	if err := ds.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce with failing listing succeeded, want error")
	}
	if !metrics.badDiskSet {
		t.Fatal("bad-disk gauge cleared despite a failing listing; suspicion must survive until a listing succeeds")
	}
	if metrics.runsError != 1 {
		t.Fatalf("error runs = %d, want 1", metrics.runsError)
	}
}

func TestDeepScrubber_CleanBlocks(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		{BlockID: 2, BlkPath: "/tmp/2.blk", IdxPath: "/tmp/2.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10},
		{FramesVerified: 20},
	}}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: qm,
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(qm.quarantined) != 0 {
		t.Fatalf("expected 0 quarantines, got %d", len(qm.quarantined))
	}
	if metrics.framesVerified != 30 {
		t.Fatalf("expected 30 frames verified, got %d", metrics.framesVerified)
	}
	if metrics.runsOK != 1 {
		t.Fatalf("expected 1 ok run, got %d", metrics.runsOK)
	}
}

func TestDeepScrubber_SkipsEvictedBlocks(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &recordingVerifier{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:          lister,
		BlockVerifier:        verifier,
		QuarantineManager:    &stubQuarantineManager{},
		Metrics:              metrics,
		BlockStateClassifier: &stubScrubBlockClassifier{states: map[uint64]scrub.BlockLocalState{1: scrub.BlockLocalStateEvicted}},
		OpenBlockID:          99,
		CorruptCap:           5,
	})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("VerifyBlock calls = %d, want 0", verifier.calls)
	}
	if metrics.skips["evicted"] != 1 {
		t.Fatalf("evicted skips = %d, want 1", metrics.skips["evicted"])
	}
	if metrics.runsOK != 1 {
		t.Fatalf("ok runs = %d, want 1", metrics.runsOK)
	}
}

func TestDeepScrubber_LifecycleFailuresMarkRunError(t *testing.T) {
	tests := []struct {
		name       string
		classifier scrub.BlockStateClassifier
	}{
		{
			name:       "classifier error",
			classifier: &stubScrubBlockClassifier{err: errors.New("marker invalid")},
		},
		{
			name: "unknown state",
			classifier: &stubScrubBlockClassifier{
				states: map[uint64]scrub.BlockLocalState{1: scrub.BlockLocalState("mystery")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := &recordingVerifier{}
			metrics := &deepScrubMetrics{}
			ds := scrub.NewDeep(scrub.DeepConfig{
				BlockLister:          &stubBlockLister{blocks: []block.Info{{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"}}},
				BlockVerifier:        verifier,
				QuarantineManager:    &stubQuarantineManager{},
				Metrics:              metrics,
				BlockStateClassifier: tt.classifier,
				OpenBlockID:          99,
				CorruptCap:           5,
			})

			if err := ds.RunOnce(context.Background()); err == nil {
				t.Fatal("expected lifecycle failure")
			}
			if verifier.calls != 0 {
				t.Fatalf("VerifyBlock calls = %d, want 0", verifier.calls)
			}
			if metrics.runsError != 1 {
				t.Fatalf("error runs = %d, want 1", metrics.runsError)
			}
		})
	}
}

func TestDeepScrubber_LossStateBlocksSkipAndContinue(t *testing.T) {
	// A metadata_loss/unexpected_loss block has no verifiable index, so it must
	// be skipped (with an observable reason) and the run must keep verifying the
	// higher-ID blocks — never abort the whole run.
	t.Run("metadata_loss", func(t *testing.T) {
		assertLossStateSkipped(t, scrub.BlockLocalStateMetadataLoss, "metadata_loss")
	})
	t.Run("unexpected_loss", func(t *testing.T) {
		assertLossStateSkipped(t, scrub.BlockLocalStateUnexpectedLoss, "unexpected_loss")
	})
}

func assertLossStateSkipped(t *testing.T, state scrub.BlockLocalState, reason string) {
	t.Helper()
	verifier := &recordingVerifier{}
	metrics := &deepScrubMetrics{}
	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister: &stubBlockLister{blocks: []block.Info{
			{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
			{BlockID: 2, BlkPath: "/tmp/2.blk", IdxPath: "/tmp/2.idx"},
		}},
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		Checkpoint:        &stubCheckpointStore{},
		BlockStateClassifier: &stubScrubBlockClassifier{states: map[uint64]scrub.BlockLocalState{
			1: state,
			2: scrub.BlockLocalStateHot,
		}},
		OpenBlockID: 99,
		CorruptCap:  5,
	})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("VerifyBlock calls = %d, want 1 (block 2 still verified)", verifier.calls)
	}
	if metrics.skips[reason] != 1 {
		t.Fatalf("%s skips = %d, want 1", reason, metrics.skips[reason])
	}
	if metrics.runsError != 0 {
		t.Fatalf("error runs = %d, want 0", metrics.runsError)
	}
	if metrics.runsOK != 1 {
		t.Fatalf("ok runs = %d, want 1", metrics.runsOK)
	}
}

func TestDeepScrubber_HotCleanupBlocksStillVerify(t *testing.T) {
	verifier := &recordingVerifier{}
	metrics := &deepScrubMetrics{}
	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister: &stubBlockLister{blocks: []block.Info{
			{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		}},
		BlockVerifier:        verifier,
		QuarantineManager:    &stubQuarantineManager{},
		Metrics:              metrics,
		BlockStateClassifier: &stubScrubBlockClassifier{states: map[uint64]scrub.BlockLocalState{1: scrub.BlockLocalStateHotCleanupNeeded}},
		OpenBlockID:          99,
		CorruptCap:           5,
	})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("VerifyBlock calls = %d, want 1", verifier.calls)
	}
}

func TestDeepScrubber_CorruptBlockQuarantined(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{
			FramesVerified: 5,
			CorruptFrames: []block.CorruptFrame{
				{Offset: 100, Type: block.CorruptionFrameCRC},
			},
		},
	}}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: qm,
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(qm.quarantined) != 1 {
		t.Fatalf("expected 1 quarantine, got %d", len(qm.quarantined))
	}
	if metrics.quarantines != 1 {
		t.Fatalf("expected 1 quarantine metric, got %d", metrics.quarantines)
	}
	if metrics.corruptionsCRC != 1 {
		t.Fatalf("expected 1 CRC corruption, got %d", metrics.corruptionsCRC)
	}
}

func TestDeepScrubber_QuarantineFailureMarksRunError(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{
			FramesVerified: 5,
			CorruptFrames: []block.CorruptFrame{
				{Offset: 100, Type: block.CorruptionFrameCRC},
			},
		},
	}}
	qm := &stubQuarantineManager{err: errors.New("rename denied")}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: qm,
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected quarantine failure to fail the run")
	}
	if len(qm.quarantined) != 0 {
		t.Fatalf("expected failed quarantine not to be recorded, got %d", len(qm.quarantined))
	}
	if metrics.quarantines != 0 {
		t.Fatalf("expected 0 quarantine metrics on failure, got %d", metrics.quarantines)
	}
	if metrics.runsError != 1 {
		t.Fatalf("expected 1 error run, got %d", metrics.runsError)
	}
	if metrics.runsOK != 0 {
		t.Fatalf("expected 0 ok runs, got %d", metrics.runsOK)
	}
}

func TestDeepScrubber_CorruptCapEscalation_BadDisk(t *testing.T) {
	corruptFrames := make([]block.CorruptFrame, 6)
	for i := range corruptFrames {
		corruptFrames[i] = block.CorruptFrame{
			Offset: int64(i * 100),
			Type:   block.CorruptionFrameCRC,
		}
	}

	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10, CorruptFrames: corruptFrames},
	}}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: qm,
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !metrics.badDiskSet {
		t.Fatal("expected bad_disk_suspected to be set")
	}
	if len(qm.quarantined) != 1 {
		t.Fatalf("expected 1 quarantine (entire block), got %d", len(qm.quarantined))
	}
}

func TestDeepScrubber_StartStop(t *testing.T) {
	lister := &stubBlockLister{}
	verifier := &orderedVerifier{}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: qm,
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
		Interval:          50 * time.Millisecond,
		Jitter:            0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	ds.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	cancel()
	ds.Stop()

	if metrics.runsOK < 1 {
		t.Fatalf("expected at least 1 run, got %d", metrics.runsOK)
	}
}

func TestDeepScrubber_CheckpointResumesFromLastBlock(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		{BlockID: 2, BlkPath: "/tmp/2.blk", IdxPath: "/tmp/2.idx"},
		{BlockID: 3, BlkPath: "/tmp/3.blk", IdxPath: "/tmp/3.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10},
	}}
	checkpoint := &stubCheckpointStore{blockID: 2, set: true}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		Checkpoint:        checkpoint,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.framesVerified != 10 {
		t.Fatalf("expected 10 frames (only block 3), got %d", metrics.framesVerified)
	}
}

func TestDeepScrubber_CheckpointClearedOnCompletion(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 5},
	}}
	checkpoint := &stubCheckpointStore{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		Checkpoint:        checkpoint,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if checkpoint.set {
		t.Fatal("expected checkpoint to be cleared after full cycle")
	}
}

func TestDeepScrubber_LatencyPauseFires(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 5},
	}}
	metrics := &deepScrubMetrics{}
	signal := &stubLatencySignal{p99: 50 * time.Millisecond}

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		LatencySignal:     signal,
		PauseThreshold:    10 * time.Millisecond,
		PauseCooldown:     10 * time.Millisecond,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	err := ds.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.pauses != 1 {
		t.Fatalf("expected 1 pause, got %d", metrics.pauses)
	}
}

func TestDeepScrubber_PressurePauseWaitsUntilResume(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 5},
	}}
	metrics := &deepScrubMetrics{}
	pause := newControlledPause()

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		PauseController:   pause,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	done := make(chan error, 1)
	go func() {
		done <- ds.RunOnce(context.Background())
	}()

	select {
	case <-pause.waiting:
	case <-time.After(time.Second):
		t.Fatal("deep scrubber did not wait on pressure pause")
	}
	if metrics.framesVerified != 0 {
		t.Fatalf("frames verified while paused = %d, want 0", metrics.framesVerified)
	}

	pause.Resume()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deep scrubber did not resume after pressure pause cleared")
	}
	if metrics.pauses != 1 {
		t.Fatalf("expected 1 pause metric, got %d", metrics.pauses)
	}
	if metrics.framesVerified != 5 {
		t.Fatalf("frames verified = %d, want 5", metrics.framesVerified)
	}
}

func TestDeepScrubber_IOBudgetThrottles(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "test.blk")
	if err := os.WriteFile(blkPath, make([]byte, 1000), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	lister := &stubBlockLister{blocks: []block.Info{
		{BlockID: 1, BlkPath: blkPath, IdxPath: "/tmp/1.idx"},
		{BlockID: 2, BlkPath: blkPath, IdxPath: "/tmp/2.idx"},
		{BlockID: 3, BlkPath: blkPath, IdxPath: "/tmp/3.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 1},
		{FramesVerified: 1},
		{FramesVerified: 1},
	}}
	metrics := &deepScrubMetrics{}
	budget := scrub.NewTokenBucket(1500)

	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       lister,
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		IOBudget:          budget,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	start := time.Now()
	err := ds.RunOnce(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if elapsed < 500*time.Millisecond {
		t.Fatalf("expected IO budget to throttle (3x1000 bytes at 1500 B/s), elapsed %v", elapsed)
	}
}

type stubLatencySignal struct {
	p99 time.Duration
}

func (s *stubLatencySignal) ReadP99() time.Duration {
	return s.p99
}

// --- Repair tests ---

func TestDeepScrubber_RunsRepairBeforeScan(t *testing.T) {
	order := make([]string, 0, 2)
	repairer := &stubBlockRepairer{record: func() {
		order = append(order, "repair")
	}}
	verifier := &recordingVerifier{record: func() {
		order = append(order, "verify")
	}}
	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister: &stubBlockLister{blocks: []block.Info{
			{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		}},
		BlockVerifier:     verifier,
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           &deepScrubMetrics{},
		BlockRepairer:     repairer,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if repairer.calls != 1 {
		t.Fatalf("repair calls = %d, want 1", repairer.calls)
	}
	if len(order) != 2 || order[0] != "repair" || order[1] != "verify" {
		t.Fatalf("order = %v, want [repair verify]", order)
	}
}

func TestDeepScrubber_NilRepairerSkipsGracefully(t *testing.T) {
	metrics := &deepScrubMetrics{}
	ds := scrub.NewDeep(scrub.DeepConfig{
		BlockLister:       &stubBlockLister{},
		BlockVerifier:     &orderedVerifier{},
		QuarantineManager: &stubQuarantineManager{},
		Metrics:           metrics,
		OpenBlockID:       99,
		CorruptCap:        5,
	})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.repairsOK+metrics.repairsFailed != 0 {
		t.Fatal("expected no repair attempts without repairer")
	}
}
