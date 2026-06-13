package shard

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

func TestUploadPressureCoordinatorPausesScrubThroughWaitSurface(t *testing.T) {
	coordinator := newUploadPressureCoordinator()
	controller := newUploadController(nil, UploadConfig{
		Concurrency: 1,
		Pressure: UploadPressureConfig{
			BudgetBytes: 100,
			WarnPct:     0.80,
			PressurePct: 0.90,
			CriticalPct: 0.95,
		},
	}, 7, slog.New(slog.DiscardHandler), noopWriteTelemetry{}, coordinator)

	controller.SetPressure(uploadPressureStats{pendingBytes: 96, pendingBlocks: 1})
	pause := coordinator.ScrubPauseController()
	if pause == nil || !pause.IsPaused() {
		t.Fatal("expected scrub pause controller to pause at critical upload pressure")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- pause.Wait(ctx)
	}()

	select {
	case err := <-waitErr:
		t.Fatalf("pause wait returned before pressure cleared: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	controller.SetPressure(uploadPressureStats{})
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("pause wait after pressure clear: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scrub pause controller to resume")
	}
}

func TestPressurePauseGateConcurrentTransitionsAndCancellation(t *testing.T) {
	gate := newPressurePauseGate()
	gate.SetPaused(true)

	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- gate.Wait(ctx)
	}()
	cancel()
	if err := <-waitErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}

	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(paused bool) {
			defer wg.Done()
			gate.SetPaused(paused)
		}(i%2 == 0)
	}
	wg.Wait()

	gate.SetPaused(false)
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after final resume: %v", err)
	}
}

func TestUploadControllerNotifyAfterStopDoesNotCloseChannel(_ *testing.T) {
	core := newUploadControllerBoundaryCore(memoryUploadSource{})
	controller := newUploadController(core, UploadConfig{Enabled: true}, 7, slog.New(slog.DiscardHandler), noopWriteTelemetry{}, newUploadPressureCoordinator())

	controller.Start()
	controller.Stop()
	for range 16 {
		controller.Notify()
	}
}

func TestShardLocalUploadSourceReportsUnsafeLifecycle(t *testing.T) {
	tests := []struct {
		name  string
		stage func(*testing.T, string, uint64)
		want  uploadLocalAvailabilityStatus
	}{
		{
			name:  "hot Block is ready",
			stage: stageHotUploadLifecycle,
			want:  uploadLocalAvailabilityReady,
		},
		{
			name:  "metadata loss skips upload",
			stage: stageMetadataLossUploadLifecycle,
			want:  uploadLocalAvailabilityMetadataLoss,
		},
		{
			name:  "unexpected loss skips upload",
			stage: stageUnexpectedLossUploadLifecycle,
			want:  uploadLocalAvailabilityUnexpectedLoss,
		},
		{
			name:  "evicted skips upload",
			stage: stageEvictedUploadLifecycle,
			want:  uploadLocalAvailabilityEvicted,
		},
		{
			name:  "quarantined skips upload",
			stage: stageQuarantinedUploadLifecycle,
			want:  uploadLocalAvailabilityQuarantined,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.stage(t, dir, 1)
			s := &Shard{blocksDir: dir}

			source, availability := s.localUploadSource(1)
			if availability.status != tt.want {
				t.Fatalf("availability = %s, want %s", availability.status, tt.want)
			}
			if availability.ready() && source == nil {
				t.Fatal("ready availability returned nil source")
			}
			if !availability.ready() && source != nil {
				t.Fatalf("unsafe availability %s returned source", availability.status)
			}
		})
	}
}

func stageHotUploadLifecycle(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeUploadLifecycleFile(t, block.FilePath(dir, blockID))
	writeUploadLifecycleFile(t, block.IdxFilePath(dir, blockID))
}

func stageMetadataLossUploadLifecycle(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeUploadLifecycleFile(t, block.FilePath(dir, blockID))
}

func stageUnexpectedLossUploadLifecycle(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeUploadLifecycleFile(t, block.IdxFilePath(dir, blockID))
}

func stageEvictedUploadLifecycle(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeUploadLifecycleFile(t, block.IdxFilePath(dir, blockID))
	if err := WriteEvictionMarker(dir, EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/0/1.blk",
		SizeBytes:       12,
		ValidationToken: "validation",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
}

func stageQuarantinedUploadLifecycle(t *testing.T, dir string, blockID uint64) {
	t.Helper()
	writeUploadLifecycleFile(t, block.FilePath(dir, blockID))
	writeUploadLifecycleFile(t, block.IdxFilePath(dir, blockID))
	if err := block.Quarantine(block.FilePath(dir, blockID)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
}

func TestUploadControllerSkipsUnsafeLocalLifecycleBeforeBackendPut(t *testing.T) {
	store := newSuccessfulBoundaryBackend()
	core := &uploadControllerBoundaryCore{sources: map[uint64]uploadLocalSource{}}
	controller := newUploadController(core, UploadConfig{
		Backend:        store,
		Concurrency:    1,
		RetryBaseDelay: time.Nanosecond,
	}, 7, slog.New(slog.DiscardHandler), noopWriteTelemetry{}, newUploadPressureCoordinator())

	controller.processPendingOnce(context.Background())

	if got := len(store.objects); got != 0 {
		t.Fatalf("backend objects = %d, want 0 for unsafe local lifecycle", got)
	}
	if got := len(core.acceptedProposals); got != 0 {
		t.Fatalf("accepted ConfirmUpload proposals = %d, want 0", got)
	}
	status, ok := controller.localUploadUnavailableForTest(uploadApplyTestBlockID)
	if !ok || status != uploadLocalAvailabilityMetadataLoss {
		t.Fatalf("local availability = %s/%v, want metadata_loss/true", status, ok)
	}
}

func writeUploadLifecycleFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("upload lifecycle"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (c *uploadController) localUploadUnavailableForTest(blockID uint64) (uploadLocalAvailabilityStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	status, ok := c.localAvailability[blockID]
	return status, ok
}
