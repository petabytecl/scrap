package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
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
	source := &scannerProgressTestIndexSource{idx: idx}
	firstEngine := &scannerProgressRecordingEngine{}
	first := newScannerCoordinator(core, blocksDir, scannerProgressStore{source: source}, 7, ScannerConfig{
		Engine:                   firstEngine,
		SignatureVersionProvider: scannerProgressSignatureVersion("daily-2026.06.12:1"),
		Interval:                 time.Hour,
	}, nil, nil)
	if err := first.scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if got, want := firstEngine.blockIDs(), []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first scanned Blocks = %v, want %v", got, want)
	}

	createScannerProgressTestBlock(t, blocksDir, 3)

	secondEngine := &scannerProgressRecordingEngine{}
	second := newScannerCoordinator(core, blocksDir, scannerProgressStore{source: source}, 7, ScannerConfig{
		Engine:                   secondEngine,
		SignatureVersionProvider: scannerProgressSignatureVersion("daily-2026.06.12:1"),
		Interval:                 time.Hour,
	}, nil, nil)
	if err := second.scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got, want := secondEngine.blockIDs(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second scanned Blocks = %v, want %v", got, want)
	}
}

func TestScannerProgressStoreUsesCurrentProjectionAfterSwap(t *testing.T) {
	dir := t.TempDir()
	first, err := index.Open(filepath.Join(dir, "first"))
	if err != nil {
		t.Fatalf("Open first index: %v", err)
	}
	source := &scannerProgressTestIndexSource{idx: first}
	store := scannerProgressStore{source: source}

	progress := avscan.Progress{
		LastScannedBlockID:          2,
		LastSignatureVersionScanned: "daily-2026.06.12:1",
	}
	if err := store.SaveScannerProgress(context.Background(), progress); err != nil {
		t.Fatalf("Save first progress: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first index: %v", err)
	}

	second, err := index.Open(filepath.Join(dir, "second"))
	if err != nil {
		t.Fatalf("Open second index: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	source.set(second)

	if _, err := store.LoadScannerProgress(context.Background()); !errors.Is(err, avscan.ErrProgressNotFound) {
		t.Fatalf("Load after swap error = %v, want ErrProgressNotFound", err)
	}
	if err := store.SaveScannerProgress(context.Background(), progress); err != nil {
		t.Fatalf("Save after swap: %v", err)
	}
	got, err := second.GetScannerWatermark()
	if err != nil {
		t.Fatalf("GetScannerWatermark after swap: %v", err)
	}
	if got.LastScannedBlockID != 2 || got.LastSignatureVersionScanned != progress.LastSignatureVersionScanned {
		t.Fatalf("watermark after swap = %+v, want %+v", got, progress)
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

type scannerProgressTestIndexSource struct {
	mu  sync.Mutex
	idx *index.Index
}

func (s *scannerProgressTestIndexSource) withScannerProjection(use func(*index.Index) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return use(s.idx)
}

func (s *scannerProgressTestIndexSource) set(idx *index.Index) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idx = idx
}
