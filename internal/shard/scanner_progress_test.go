package shard

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestScannerCoordinatorPersistsProgressAcrossReconstruction(t *testing.T) {
	dir := t.TempDir()
	blocksDir := filepath.Join(dir, "blocks")
	pebbleDir := filepath.Join(dir, "pebble")

	if err := ensureShardDirs(blocksDir, pebbleDir); err != nil {
		t.Fatalf("ensureShardDirs: %v", err)
	}
	idx, err := index.Open(pebbleDir)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	createScannerProgressTestBlock(t, blocksDir, 1)
	createScannerProgressTestBlock(t, blocksDir, 2)

	core := scannerProgressTestCore{leader: true, openBlockID: 99}
	firstEngine := &scannerProgressRecordingEngine{}
	first := newScannerCoordinator(core, blocksDir, idx, 7, ScannerConfig{
		Engine:                   firstEngine,
		SignatureVersionProvider: scannerProgressSignatureVersion("daily-2026.06.12:1"),
		Interval:                 time.Hour,
	}, nil)
	if err := first.scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got, want := firstEngine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first scanned Blocks = %v, want %v", got, want)
	}

	createScannerProgressTestBlock(t, blocksDir, 3)

	secondEngine := &scannerProgressRecordingEngine{}
	second := newScannerCoordinator(core, blocksDir, idx, 7, ScannerConfig{
		Engine:                   secondEngine,
		SignatureVersionProvider: scannerProgressSignatureVersion("daily-2026.06.12:1"),
		Interval:                 time.Hour,
	}, nil)
	if err := second.scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := secondEngine.blockIDs(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second scanned Blocks = %v, want %v", got, want)
	}
}

func createScannerProgressTestBlock(t *testing.T, dir string, blockID uint64) {
	t.Helper()

	bw, err := block.NewWriter(block.FilePath(dir, blockID), 7, blockID)
	if err != nil {
		t.Fatalf("NewWriter(%d): %v", blockID, err)
	}
	body := bytes.NewReader(bytes.Repeat([]byte("x"), 128))
	if _, err := bw.AppendDocument("tx-scanner-progress", fmt.Sprintf("doc-%d.bin", blockID), "application/octet-stream", body); err != nil {
		t.Fatalf("AppendDocument(%d): %v", blockID, err)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close block(%d): %v", blockID, err)
	}

	iw, err := block.NewIndexWriter(block.IdxFilePath(dir, blockID))
	if err != nil {
		t.Fatalf("NewIndexWriter(%d): %v", blockID, err)
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close idx(%d): %v", blockID, err)
	}
}

type scannerProgressTestCore struct {
	leader      bool
	openBlockID uint64
}

func (c scannerProgressTestCore) IsLeader() bool {
	return c.leader
}

func (c scannerProgressTestCore) currentOpenBlockID() uint64 {
	return c.openBlockID
}

type scannerProgressRecordingEngine struct {
	scanned []avscan.Block
}

func (e *scannerProgressRecordingEngine) Scan(_ context.Context, block avscan.Block) (avscan.Result, error) {
	e.scanned = append(e.scanned, block)
	return avscan.Result{Status: avscan.ResultClean, ScannedDocuments: 1}, nil
}

func (e *scannerProgressRecordingEngine) blockIDs() []uint64 {
	ids := make([]uint64, 0, len(e.scanned))
	for _, block := range e.scanned {
		ids = append(ids, block.BlockID)
	}
	return ids
}

type scannerProgressSignatureVersion string

func (v scannerProgressSignatureVersion) SignatureVersion(context.Context) (string, error) {
	return string(v), nil
}
