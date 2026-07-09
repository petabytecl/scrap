package shard

import (
	"context"
	"testing"
	"time"
)

func TestDetachedRestoreContextAppliesMandatoryTimeout(t *testing.T) {
	s := &Shard{upload: UploadConfig{RestoreTimeout: 50 * time.Millisecond}}
	ctx, cancel := s.detachedRestoreContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("restore context missing deadline")
	}
	if remaining := time.Until(deadline); remaining > 100*time.Millisecond || remaining <= 0 {
		t.Fatalf("restore deadline remaining = %v, want ~50ms", remaining)
	}
}

func TestRestoreSemaphoreBoundsConcurrency(t *testing.T) {
	s := &Shard{restoreSem: newRestoreSemaphore(1)}
	ctx := context.Background()
	if err := s.acquireRestoreSlot(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	blocked := make(chan error, 1)
	go func() {
		blocked <- s.acquireRestoreSlot(ctx)
	}()

	select {
	case err := <-blocked:
		t.Fatalf("second acquire returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	s.releaseRestoreSlot()
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("second acquire after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second acquire")
	}
	s.releaseRestoreSlot()
}
