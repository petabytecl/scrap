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
	blocks []block.BlockInfo
}

func (s *stubBlockLister) ListSealedBlocks(openBlockID uint64) ([]block.BlockInfo, error) {
	var out []block.BlockInfo
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
	quarantined    []string
	quarantinedIDs []uint64
	listErr        error
	err            error
}

func (s *stubQuarantineManager) Quarantine(blkPath string) error {
	if s.err != nil {
		return s.err
	}
	s.quarantined = append(s.quarantined, blkPath)
	return nil
}

func (s *stubQuarantineManager) ListQuarantined() ([]uint64, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.quarantinedIDs, nil
}

type repairCall struct {
	blockID  uint64
	peerAddr string
}

type stubBlockRepairer struct {
	calls []repairCall
	err   error
}

func (s *stubBlockRepairer) RepairFromPeer(_ context.Context, blockID uint64, peerAddr string) error {
	s.calls = append(s.calls, repairCall{blockID: blockID, peerAddr: peerAddr})
	return s.err
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

func TestDeepScrubber_CleanBlocks(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.BlockInfo{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		{BlockID: 2, BlkPath: "/tmp/2.blk", IdxPath: "/tmp/2.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10},
		{FramesVerified: 20},
	}}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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

func TestDeepScrubber_CorruptBlockQuarantined(t *testing.T) {
	lister := &stubBlockLister{blocks: []block.BlockInfo{
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

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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
	lister := &stubBlockLister{blocks: []block.BlockInfo{
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

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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

	lister := &stubBlockLister{blocks: []block.BlockInfo{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10, CorruptFrames: corruptFrames},
	}}
	qm := &stubQuarantineManager{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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
	lister := &stubBlockLister{blocks: []block.BlockInfo{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
		{BlockID: 2, BlkPath: "/tmp/2.blk", IdxPath: "/tmp/2.idx"},
		{BlockID: 3, BlkPath: "/tmp/3.blk", IdxPath: "/tmp/3.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 10},
	}}
	checkpoint := &stubCheckpointStore{blockID: 2, set: true}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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
	lister := &stubBlockLister{blocks: []block.BlockInfo{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 5},
	}}
	checkpoint := &stubCheckpointStore{}
	metrics := &deepScrubMetrics{}

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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
	lister := &stubBlockLister{blocks: []block.BlockInfo{
		{BlockID: 1, BlkPath: "/tmp/1.blk", IdxPath: "/tmp/1.idx"},
	}}
	verifier := &orderedVerifier{results: []block.VerifyResult{
		{FramesVerified: 5},
	}}
	metrics := &deepScrubMetrics{}
	signal := &stubLatencySignal{p99: 50 * time.Millisecond}

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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

func TestDeepScrubber_IOBudgetThrottles(t *testing.T) {
	dir := t.TempDir()
	blkPath := filepath.Join(dir, "test.blk")
	if err := os.WriteFile(blkPath, make([]byte, 1000), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	lister := &stubBlockLister{blocks: []block.BlockInfo{
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

	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
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

func newRepairScrubber(qm *stubQuarantineManager, repairer *stubBlockRepairer, metrics *deepScrubMetrics, peers []string) *scrub.DeepScrubber {
	return scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
		BlockLister:       &stubBlockLister{},
		BlockVerifier:     &orderedVerifier{},
		QuarantineManager: qm,
		Metrics:           metrics,
		BlockRepairer:     repairer,
		PeerAddrs:         peers,
		OpenBlockID:       99,
		CorruptCap:        5,
	})
}

func TestDeepScrubber_RepairsQuarantinedBeforeScan(t *testing.T) {
	qm := &stubQuarantineManager{quarantinedIDs: []uint64{5}}
	repairer := &stubBlockRepairer{}
	metrics := &deepScrubMetrics{}
	ds := newRepairScrubber(qm, repairer, metrics, []string{"peer-1:9091", "peer-2:9091"})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(repairer.calls) != 1 {
		t.Fatalf("expected 1 repair call, got %d", len(repairer.calls))
	}
	if repairer.calls[0].blockID != 5 {
		t.Fatalf("expected repair for block 5, got %d", repairer.calls[0].blockID)
	}
	if metrics.repairsOK != 1 {
		t.Fatalf("expected 1 repair ok, got %d", metrics.repairsOK)
	}
	if metrics.decremented != 1 {
		t.Fatalf("expected 1 gauge decrement, got %d", metrics.decremented)
	}
}

func TestDeepScrubber_FailedRepairEmitsMetric(t *testing.T) {
	qm := &stubQuarantineManager{quarantinedIDs: []uint64{5}}
	repairer := &stubBlockRepairer{err: errors.New("peer unreachable")}
	metrics := &deepScrubMetrics{}
	ds := newRepairScrubber(qm, repairer, metrics, []string{"peer-1:9091"})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if metrics.repairsFailed != 1 {
		t.Fatalf("expected 1 failed repair, got %d", metrics.repairsFailed)
	}
	if metrics.repairsOK != 0 {
		t.Fatalf("expected 0 ok repairs, got %d", metrics.repairsOK)
	}
	if metrics.decremented != 0 {
		t.Fatalf("expected 0 gauge decrements, got %d", metrics.decremented)
	}
}

func TestDeepScrubber_RoundRobinPeerRotation(t *testing.T) {
	qm := &stubQuarantineManager{quarantinedIDs: []uint64{5}}
	repairer := &stubBlockRepairer{}
	metrics := &deepScrubMetrics{}
	ds := newRepairScrubber(qm, repairer, metrics, []string{"peer-A", "peer-B", "peer-C"})

	_ = ds.RunOnce(context.Background())
	_ = ds.RunOnce(context.Background())
	_ = ds.RunOnce(context.Background())

	if len(repairer.calls) != 3 {
		t.Fatalf("expected 3 repair calls, got %d", len(repairer.calls))
	}
	peers := []string{repairer.calls[0].peerAddr, repairer.calls[1].peerAddr, repairer.calls[2].peerAddr}
	if peers[0] == peers[1] || peers[1] == peers[2] {
		t.Fatalf("expected rotating peers, got %v", peers)
	}
}

func TestDeepScrubber_NilRepairerSkipsGracefully(t *testing.T) {
	qm := &stubQuarantineManager{quarantinedIDs: []uint64{5}}
	metrics := &deepScrubMetrics{}
	ds := scrub.NewDeepScrubber(scrub.DeepScrubberConfig{
		BlockLister:       &stubBlockLister{},
		BlockVerifier:     &orderedVerifier{},
		QuarantineManager: qm,
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

func TestDeepScrubber_ListQuarantinedErrorSkipsRepair(t *testing.T) {
	qm := &stubQuarantineManager{
		quarantinedIDs: []uint64{5},
		listErr:        errors.New("permission denied"),
	}
	repairer := &stubBlockRepairer{}
	metrics := &deepScrubMetrics{}
	ds := newRepairScrubber(qm, repairer, metrics, []string{"peer-1:9091"})

	if err := ds.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(repairer.calls) != 0 {
		t.Fatalf("expected 0 repair calls when list fails, got %d", len(repairer.calls))
	}
	if metrics.repairsOK+metrics.repairsFailed != 0 {
		t.Fatal("expected no repair metrics when list fails")
	}
	if metrics.runsOK != 1 {
		t.Fatalf("expected scan to complete ok, got %d ok runs", metrics.runsOK)
	}
}
