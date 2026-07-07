package shard

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/localblock"
)

type restoreScanCore struct {
	openBlockID uint64
}

func (c restoreScanCore) IsLeader() bool { return true }

func (c restoreScanCore) currentOpenBlockID() uint64 { return c.openBlockID }

type restoreScanProgressStore struct {
	progress avscan.Progress
}

func (s *restoreScanProgressStore) LoadScannerProgress(context.Context) (avscan.Progress, error) {
	return s.progress, nil
}

func (s *restoreScanProgressStore) SaveScannerProgress(_ context.Context, progress avscan.Progress) error {
	s.progress = progress
	return nil
}

type restoreScanRecordingEngine struct {
	scanned []uint64
}

func (e *restoreScanRecordingEngine) Scan(_ context.Context, block avscan.Block) (avscan.Result, error) {
	e.scanned = append(e.scanned, block.BlockID)
	return avscan.Result{Status: avscan.ResultClean, ScannedDocuments: 1}, nil
}

type restoreScanSignatureVersion string

func (v restoreScanSignatureVersion) SignatureVersion(context.Context) (string, error) {
	return string(v), nil
}

func newRestoreScanCoordinator(t *testing.T, blocksDir string, progress avscan.ProgressStore, engine avscan.Engine) *scannerCoordinator {
	t.Helper()
	return newScannerCoordinator(
		restoreScanCore{openBlockID: 2},
		blocksDir,
		progress,
		7,
		ScannerConfig{
			Engine:                   engine,
			SignatureVersionProvider: restoreScanSignatureVersion("sig-1"),
			Interval:                 time.Hour,
		},
		nil,
		slog.New(slog.DiscardHandler),
	)
}

// Regression for #454: a restored Block below the durable frontier must be
// rescanned exactly once per restore, not once per process restart. The
// durable record is the scanned_at_us stamp on the restore marker.
func TestScannerRestoredBlockRescansOncePerRestoreNotPerRestart(t *testing.T) {
	ctx := context.Background()
	blocksDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.blk"), []byte("sealed"), 0o600); err != nil {
		t.Fatalf("write sealed Block: %v", err)
	}
	if err := localblock.WriteRestoreMarker(blocksDir, localblock.RestoreMarker{
		BlockID:      1,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       localblock.RestoreSourceBackend,
		Reason:       localblock.RestoreReasonRead,
	}); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	// The frontier already covers Block 1: only the restore marker keeps it
	// scan-eligible.
	progress := &restoreScanProgressStore{progress: avscan.Progress{
		LastScannedBlockID:          1,
		LastSignatureVersionScanned: "sig-1",
	}}

	first := &restoreScanRecordingEngine{}
	c1 := newRestoreScanCoordinator(t, blocksDir, progress, first)
	if err := c1.scheduler.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if len(first.scanned) != 1 || first.scanned[0] != 1 {
		t.Fatalf("first process scanned = %v, want restored Block 1", first.scanned)
	}
	if _, err := localblock.ReadRestoreScanRecord(blocksDir, 1); err != nil {
		t.Fatalf("post-restore scan record not written: %v", err)
	}

	// Simulate a restart: a fresh coordinator loses the in-memory completed
	// set, so only the durable scan record can suppress the rescan.
	second := &restoreScanRecordingEngine{}
	c2 := newRestoreScanCoordinator(t, blocksDir, progress, second)
	if err := c2.scheduler.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(second.scanned) != 0 {
		t.Fatalf("post-restart process rescanned = %v, want none", second.scanned)
	}
}
