package localblock_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
)

func TestClassifyLifecycle(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		want     localblock.State
		serving  bool
		degraded bool
	}{
		{
			name: "hot",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			},
			want:    localblock.StateHot,
			serving: true,
		},
		{
			name: "evicted",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
				writeEvictionMarker(t, dir)
			},
			want: localblock.StateEvicted,
		},
		{
			name: "hot cleanup needed",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
				writeEvictionMarker(t, dir)
			},
			want:     localblock.StateHotCleanupNeeded,
			serving:  true,
			degraded: true,
		},
		{
			name: "metadata loss takes precedence over eviction marker",
			setup: func(t *testing.T, dir string) {
				writeEvictionMarker(t, dir)
			},
			want:     localblock.StateMetadataLoss,
			degraded: true,
		},
		{
			name: "metadata loss when hot block index is missing",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.FilePath(dir, 1), "block")
			},
			want:     localblock.StateMetadataLoss,
			degraded: true,
		},
		{
			name: "unexpected loss",
			setup: func(t *testing.T, dir string) {
				writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			},
			want:     localblock.StateUnexpectedLoss,
			degraded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			lifecycle, err := localblock.Classify(dir, 1)
			if err != nil {
				t.Fatalf("Classify: %v", err)
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

func TestMarkersAreVersionedJSON(t *testing.T) {
	dir := t.TempDir()

	eviction := evictionMarkerForTest(7)
	if err := localblock.WriteEvictionMarker(dir, eviction); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	assertEvictionMarkerRoundTrip(t, dir, eviction)

	restore := restoreMarkerForTest(7)
	if err := localblock.WriteRestoreMarker(dir, restore); err != nil {
		t.Fatalf("WriteRestoreMarker: %v", err)
	}
	assertRestoreMarkerRoundTrip(t, dir, restore)
}

func assertEvictionMarkerRoundTrip(t *testing.T, dir string, want localblock.EvictionMarker) {
	t.Helper()
	got, err := localblock.ReadEvictionMarker(dir, want.BlockID)
	if err != nil {
		t.Fatalf("ReadEvictionMarker: %v", err)
	}
	if got.Version != localblock.MarkerVersion {
		t.Fatalf("eviction marker version = %d, want %d", got.Version, localblock.MarkerVersion)
	}
	if got.BlockID != want.BlockID || got.BackendKey != want.BackendKey {
		t.Fatalf("eviction marker = %+v, want %+v", got, want)
	}
}

func assertRestoreMarkerRoundTrip(t *testing.T, dir string, want localblock.RestoreMarker) {
	t.Helper()
	got, err := localblock.ReadRestoreMarker(dir, want.BlockID)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if got.Version != localblock.MarkerVersion {
		t.Fatalf("restore marker version = %d, want %d", got.Version, localblock.MarkerVersion)
	}
	if got.BlockID != want.BlockID || got.Source != want.Source {
		t.Fatalf("restore marker = %+v, want %+v", got, want)
	}
}

func TestMalformedMarkersFailClosed(t *testing.T) {
	tests := []struct {
		name string
		path func(dir string) string
	}{
		{
			name: "eviction",
			path: func(dir string) string {
				return localblock.EvictionMarkerPath(dir, 1)
			},
		},
		{
			name: "restore",
			path: func(dir string) string {
				return localblock.RestoreMarkerPath(dir, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeLifecycleFile(t, block.FilePath(dir, 1), "block")
			writeLifecycleFile(t, block.IdxFilePath(dir, 1), "index")
			writeLifecycleFile(t, tt.path(dir), "{")

			_, err := localblock.Classify(dir, 1)
			if !errors.Is(err, localblock.ErrMarkerInvalid) {
				t.Fatalf("Classify error = %v, want ErrMarkerInvalid", err)
			}
		})
	}
}

func writeEvictionMarker(t *testing.T, dir string) {
	t.Helper()
	if err := localblock.WriteEvictionMarker(dir, evictionMarkerForTest(1)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}

func evictionMarkerForTest(blockID uint64) localblock.EvictionMarker {
	return localblock.EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0000000000000001/0000000000000001.blk",
		SizeBytes:       4096,
		ValidationToken: validationValueForTest(blockID),
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         localblock.EvictionTriggerOperatorRequested,
		Reason:          localblock.EvictionReasonEvidenceRun,
	}
}

func restoreMarkerForTest(blockID uint64) localblock.RestoreMarker {
	return localblock.RestoreMarker{
		BlockID:      blockID,
		RestoredAtUs: time.Unix(20, 0).UnixMicro(),
		Source:       localblock.RestoreSourceBackend,
		Reason:       localblock.RestoreReasonRead,
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
