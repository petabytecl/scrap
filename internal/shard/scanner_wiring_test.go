package shard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestShardScannerScansSealedBlocksAfterWriteAck(t *testing.T) {
	engine := newShardRecordingScanEngine(nil)
	s := openScannerTestShard(t, engine, true)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-scanner-ack", "a.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 128))); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-scanner-ack", "b.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}

	scanned := waitScannedBlock(t, engine)
	if scanned.BlockID != 1 {
		t.Fatalf("scanned BlockID = %d, want sealed Block 1", scanned.BlockID)
	}
	if scanned.SizeBytes == 0 {
		t.Fatalf("scanned Block size = 0, want nonzero")
	}
	reader, err := scanned.OpenBytes(ctx)
	if err != nil {
		t.Fatalf("open scanned Block stream: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if payload, err := io.ReadAll(io.LimitReader(reader, 1)); err != nil || len(payload) != 1 {
		t.Fatalf("read scanned Block stream = %d bytes, %v; want one byte", len(payload), err)
	}
}

func TestShardScannerMetricsUseOwningShardID(t *testing.T) {
	engine := newShardRecordingScanEngine(nil)
	metrics := newShardScannerRecordingMetrics()
	s := openScannerTestShardWithMetrics(t, engine, metrics, true)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-scanner-metrics", "a.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 128))); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-scanner-metrics", "b.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}
	_ = waitScannedBlock(t, engine)

	select {
	case shardID := <-metrics.runShardIDs:
		if shardID != 7 {
			t.Fatalf("metric shard ID = %d, want 7", shardID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scanner metrics did not record run")
	}
}

func TestShardScannerUnavailableDoesNotBlockWritesAndIsObservable(t *testing.T) {
	engine := newShardRecordingScanEngine(avscan.ErrEngineUnavailable)
	s := openScannerTestShard(t, engine, true)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-scanner-outage", "a.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 128))); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-scanner-outage", "b.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}
	_ = waitScannedBlock(t, engine)
	waitScannerReason(t, s, avscan.ReasonEngineUnavailable)

	if _, err := s.WriteDocument(ctx, "tx-scanner-outage", "c.bin", "application/octet-stream", "", bytes.NewReader([]byte("c"))); err != nil {
		t.Fatalf("write after scanner outage: %v", err)
	}
}

func TestShardCloseCancelsActiveScannerWorker(t *testing.T) {
	engine := newBlockingShardScanEngine()
	s := openScannerTestShard(t, engine, false)
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-scanner-close", "a.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 128))); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-scanner-close", "b.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("second WriteDocument: %v", err)
	}
	<-engine.entered

	closed := make(chan error, 1)
	go func() {
		closed <- s.Close()
	}()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel active scanner worker")
	}
	if !engine.contextCanceled() {
		t.Fatal("scanner engine did not observe context cancellation")
	}
}

func openScannerTestShard(t *testing.T, engine avscan.Engine, cleanup bool) *shard.Shard {
	t.Helper()
	return openScannerTestShardWithMetrics(t, engine, nil, cleanup)
}

func openScannerTestShardWithMetrics(t *testing.T, engine avscan.Engine, metrics avscan.Metrics, cleanup bool) *shard.Shard {
	t.Helper()
	s, err := shard.Open(shard.Config{
		DataDir:       t.TempDir(),
		ShardID:       7,
		RaftID:        1,
		Peers:         map[uint64]string{1: "localhost:9091"},
		BlockSealSize: 64,
		TickInterval:  10 * time.Millisecond,
		Scanner: shard.ScannerConfig{
			Engine:   engine,
			Metrics:  metrics,
			Interval: time.Hour,
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cleanup {
		t.Cleanup(func() { _ = s.Close() })
	}
	waitForTestLeader(t, s)
	return s
}

func waitScannedBlock(t *testing.T, engine *shardRecordingScanEngine) avscan.Block {
	t.Helper()
	select {
	case block := <-engine.scanned:
		return block
	case <-time.After(2 * time.Second):
		t.Fatal("scanner engine did not receive sealed Block")
		return avscan.Block{}
	}
}

func waitScannerReason(t *testing.T, s *shard.Shard, reason avscan.Reason) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.ContentScannerSnapshot().LastReason == reason {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("scanner reason = %q, want %q", s.ContentScannerSnapshot().LastReason, reason)
		}
	}
}

type shardRecordingScanEngine struct {
	err     error
	scanned chan avscan.Block
}

func newShardRecordingScanEngine(err error) *shardRecordingScanEngine {
	return &shardRecordingScanEngine{
		err:     err,
		scanned: make(chan avscan.Block, 8),
	}
}

func (e *shardRecordingScanEngine) Scan(_ context.Context, block avscan.Block) (avscan.Result, error) {
	e.scanned <- block
	if e.err != nil {
		return avscan.Result{}, e.err
	}
	return avscan.Result{Status: avscan.ResultClean, ScannedDocuments: 1}, nil
}

type blockingShardScanEngine struct {
	entered  chan struct{}
	cancelMu sync.Mutex
	canceled bool
}

func newBlockingShardScanEngine() *blockingShardScanEngine {
	return &blockingShardScanEngine{entered: make(chan struct{})}
}

func (e *blockingShardScanEngine) Scan(ctx context.Context, _ avscan.Block) (avscan.Result, error) {
	close(e.entered)
	<-ctx.Done()
	e.cancelMu.Lock()
	e.canceled = errors.Is(ctx.Err(), context.Canceled)
	e.cancelMu.Unlock()
	return avscan.Result{}, ctx.Err()
}

func (e *blockingShardScanEngine) contextCanceled() bool {
	e.cancelMu.Lock()
	defer e.cancelMu.Unlock()
	return e.canceled
}

type shardScannerRecordingMetrics struct {
	runShardIDs chan uint64
}

func newShardScannerRecordingMetrics() *shardScannerRecordingMetrics {
	return &shardScannerRecordingMetrics{runShardIDs: make(chan uint64, 8)}
}

func (m *shardScannerRecordingMetrics) RecordRun(shardID uint64, _, _ string, _ time.Duration) {
	m.runShardIDs <- shardID
}

func (m *shardScannerRecordingMetrics) RecordBlock(uint64, string, string) {}

func (m *shardScannerRecordingMetrics) RecordFailure(uint64, string) {}

func (m *shardScannerRecordingMetrics) SetLag(uint64, int) {}

func (m *shardScannerRecordingMetrics) SetInFlight(uint64, int) {}

func (m *shardScannerRecordingMetrics) RecordDuplicate(uint64) {}
