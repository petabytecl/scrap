package shard_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestClassifyLocalBlockLifecycle(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		want     shard.LocalBlockState
		serving  bool
		degraded bool
	}{
		{
			name: "hot",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			},
			want:    shard.LocalBlockStateHot,
			serving: true,
		},
		{
			name: "evicted",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
				writeEvictionMarker(t, dir)
			},
			want: shard.LocalBlockStateEvicted,
		},
		{
			name: "hot cleanup needed",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
				writeEvictionMarker(t, dir)
			},
			want:     shard.LocalBlockStateHotCleanupNeeded,
			serving:  true,
			degraded: true,
		},
		{
			name: "metadata loss takes precedence over eviction marker",
			setup: func(t *testing.T, dir string) {
				writeEvictionMarker(t, dir)
			},
			want:     shard.LocalBlockStateMetadataLoss,
			degraded: true,
		},
		{
			name: "metadata loss when hot block index is missing",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
			},
			want:     shard.LocalBlockStateMetadataLoss,
			degraded: true,
		},
		{
			name: "unexpected loss",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			},
			want:     shard.LocalBlockStateUnexpectedLoss,
			degraded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			lifecycle, err := shard.ClassifyLocalBlock(dir, 1)
			if err != nil {
				t.Fatalf("ClassifyLocalBlock: %v", err)
			}
			if lifecycle.State != tt.want {
				t.Fatalf("State = %s, want %s", lifecycle.State, tt.want)
			}
			if lifecycle.ServingAllowed != tt.serving {
				t.Fatalf("ServingAllowed = %t, want %t", lifecycle.ServingAllowed, tt.serving)
			}
			if lifecycle.HealthDegraded != tt.degraded {
				t.Fatalf("HealthDegraded = %t, want %t", lifecycle.HealthDegraded, tt.degraded)
			}
		})
	}
}

func TestLifecycleMarkersAreVersionedJSON(t *testing.T) {
	dir := t.TempDir()

	eviction := evictionMarkerForTest(7)
	if err := shard.WriteEvictionMarker(dir, eviction); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	assertEvictionMarkerRoundTrip(t, dir, eviction)

	restore := restoreMarkerForTest(7)
	if err := shard.WriteRestoreMarker(dir, restore); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	assertRestoreMarkerRoundTrip(t, dir, restore)
}

func assertEvictionMarkerRoundTrip(t *testing.T, dir string, want shard.EvictionMarker) {
	t.Helper()
	got, err := shard.ReadEvictionMarker(dir, want.BlockID)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if got.Version != shard.LifecycleMarkerVersion {
		t.Fatalf("eviction marker version = %d, want %d", got.Version, shard.LifecycleMarkerVersion)
	}
	if got.BlockID != want.BlockID || got.BackendKey != want.BackendKey {
		t.Fatalf("eviction marker = %+v, want %+v", got, want)
	}
}

func assertRestoreMarkerRoundTrip(t *testing.T, dir string, want shard.RestoreMarker) {
	t.Helper()
	got, err := shard.ReadRestoreMarker(dir, want.BlockID)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if got.Version != shard.LifecycleMarkerVersion {
		t.Fatalf("restore marker version = %d, want %d", got.Version, shard.LifecycleMarkerVersion)
	}
	if got.BlockID != want.BlockID || got.Source != want.Source {
		t.Fatalf("restore marker = %+v, want %+v", got, want)
	}
}

func TestMalformedLifecycleMarkersFailClosed(t *testing.T) {
	tests := []struct {
		name string
		path func(dir string) string
	}{
		{
			name: "eviction",
			path: func(dir string) string {
				return shard.EvictionMarkerPath(dir, 1)
			},
		},
		{
			name: "restore",
			path: func(dir string) string {
				return shard.RestoreMarkerPath(dir, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeLifecycleFile(t, block.FilePath(dir, 1), "block")
			writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			writeLifecycleFile(t, tt.path(dir), "{")

			_, err := shard.ClassifyLocalBlock(dir, 1)
			if !errors.Is(err, shard.ErrLifecycleMarkerInvalid) {
				t.Fatalf("ClassifyLocalBlock error = %v, want ErrLifecycleMarkerInvalid", err)
			}
		})
	}
}

func TestHotCleanupNeededMarkerIsRemovedInBackground(t *testing.T) {
	dir := t.TempDir()
	blocksDir := filepath.Join(dir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks: %v", err)
	}
	writeLifecycleFile(t, block.FilePath(blocksDir, 1), "block")
	writeLifecycleFile(t, block.IdxFilePath(blocksDir, 1), "index")
	writeEvictionMarker(t, blocksDir)

	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	s.WaitLifecycleCleanupForTest()
	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); !os.IsNotExist(err) {
		t.Fatalf("eviction marker after cleanup: %v, want not exist", err)
	}

	lifecycle, err := shard.ClassifyLocalBlock(blocksDir, 1)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != shard.LocalBlockStateHot {
		t.Fatalf("State = %s, want %s", lifecycle.State, shard.LocalBlockStateHot)
	}
}

func writeEvictionMarker(t *testing.T, dir string) {
	t.Helper()
	if err := shard.WriteEvictionMarker(dir, evictionMarkerForTest(1)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}

func evictionMarkerForTest(blockID uint64) shard.EvictionMarker {
	return shard.EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0000000000000001/0000000000000001.blk",
		SizeBytes:       4096,
		ValidationToken: validationValueForTest(blockID),
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}
}

func restoreMarkerForTest(blockID uint64) shard.RestoreMarker {
	return shard.RestoreMarker{
		BlockID:      blockID,
		RestoredAtUs: time.Unix(20, 0).UnixMicro(),
		Source:       shard.RestoreSourceBackend,
		Reason:       shard.RestoreReasonRead,
	}
}

func validationValueForTest(blockID uint64) string {
	return "etag-block-" + strconv.FormatUint(blockID, 10)
}

func writeLifecycleFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
