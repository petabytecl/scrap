package shard

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
)

// newTestUploadController builds a controller for the concurrency/retry unit
// tests. These methods never touch the core seam, so a nil core is fine.
func newTestUploadController(cfg UploadConfig) *uploadController {
	return newUploadController(nil, cfg, 0, slog.New(slog.DiscardHandler), nil, nil)
}

func TestUploadThrottleReducesAndSuccessesRestoreConcurrency(t *testing.T) {
	c := newTestUploadController(UploadConfig{Concurrency: 3})
	c.resetConcurrency()

	c.recordThrottle()
	if got := c.concurrency(); got != 2 {
		t.Fatalf("concurrency after first throttle = %d, want 2", got)
	}
	c.recordThrottle()
	if got := c.concurrency(); got != 1 {
		t.Fatalf("concurrency after second throttle = %d, want 1", got)
	}
	c.recordThrottle()
	if got := c.concurrency(); got != 1 {
		t.Fatalf("concurrency floor after third throttle = %d, want 1", got)
	}

	for range successesToRestoreUpload {
		c.recordSuccess()
	}
	if got := c.concurrency(); got != 2 {
		t.Fatalf("concurrency after five successes = %d, want 2", got)
	}
}

func TestUploadRequeueMovesFailedBlocksToBack(t *testing.T) {
	c := newTestUploadController(UploadConfig{})
	c.markRequeued(1)

	ordered := c.orderPending([]PendingUpload{
		{BlockID: 1},
		{BlockID: 2},
		{BlockID: 3},
	})

	got := []uint64{ordered[0].BlockID, ordered[1].BlockID, ordered[2].BlockID}
	want := []uint64{2, 3, 1}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ordered[%d] = %d, want %d (full order %v)", i, got[i], want[i], got)
		}
	}
}

func TestHandleUploadRetryThrottled(t *testing.T) {
	ctx := context.Background()
	c := newRetryTestController()
	c.resetConcurrency()

	throttled := newUploadRetryState(time.Nanosecond)
	retry, err := c.handleRetry(ctx, backend.ErrThrottled, &throttled)
	if err != nil || !retry {
		t.Fatalf("throttled retry = %v, err = %v; want retry without error", retry, err)
	}
	if got := c.concurrency(); got != 1 {
		t.Fatalf("throttled concurrency = %d, want 1", got)
	}
}

func TestHandleUploadRetryTransient(t *testing.T) {
	ctx := context.Background()
	c := newRetryTestController()

	transient := newUploadRetryState(time.Nanosecond)
	retry, err := c.handleRetry(ctx, backend.ErrTransient, &transient)
	if err != nil || !retry {
		t.Fatalf("transient retry = %v, err = %v; want retry without error", retry, err)
	}
	transient.transientAttempts = maxTransientUploadRetries
	retry, err = c.handleRetry(ctx, backend.ErrTransient, &transient)
	if err != nil || retry {
		t.Fatalf("exhausted transient retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func TestHandleUploadRetryNotFound(t *testing.T) {
	ctx := context.Background()
	c := newRetryTestController()

	notFound := newUploadRetryState(time.Nanosecond)
	retry, err := c.handleRetry(ctx, backend.ErrNotFound, &notFound)
	if err != nil || !retry {
		t.Fatalf("not-found retry = %v, err = %v; want retry without error", retry, err)
	}
	notFound.transientAttempts = maxTransientUploadRetries
	retry, err = c.handleRetry(ctx, backend.ErrNotFound, &notFound)
	if err != nil || retry {
		t.Fatalf("exhausted not-found retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func TestHandleUploadRetryCorrupt(t *testing.T) {
	ctx := context.Background()
	c := newRetryTestController()

	corrupt := newUploadRetryState(time.Nanosecond)
	retry, err := c.handleRetry(ctx, backend.ErrCorrupt, &corrupt)
	if err != nil || !retry {
		t.Fatalf("first corrupt retry = %v, err = %v; want retry without error", retry, err)
	}
	retry, err = c.handleRetry(ctx, backend.ErrCorrupt, &corrupt)
	if err != nil || retry {
		t.Fatalf("second corrupt retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func TestHandleUploadRetryAuthAndPermanent(t *testing.T) {
	ctx := context.Background()
	c := newRetryTestController()
	state := newUploadRetryState(time.Nanosecond)

	retry, err := c.handleRetry(ctx, backend.ErrAuth, &state)
	if err != nil || retry {
		t.Fatalf("auth retry = %v, err = %v; want pause without retry error", retry, err)
	}

	retry, err = c.handleRetry(ctx, backend.ErrPermanent, &state)
	if err != nil || retry {
		t.Fatalf("permanent retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func newRetryTestController() *uploadController {
	return newTestUploadController(UploadConfig{
		Concurrency:    2,
		RetryBaseDelay: time.Nanosecond,
		AuthRetryDelay: time.Nanosecond,
	})
}

func TestUploadConfigDefaults(t *testing.T) {
	c := newTestUploadController(UploadConfig{})

	if got := c.configuredConcurrency(); got != DefaultUploadConcurrency {
		t.Fatalf("configuredConcurrency = %d, want %d", got, DefaultUploadConcurrency)
	}
	if got := c.cellID(); got != "local" {
		t.Fatalf("cellID = %q, want local", got)
	}
	if got := c.retryBaseDelay(); got != defaultUploadRetryBase {
		t.Fatalf("retryBaseDelay = %s, want %s", got, defaultUploadRetryBase)
	}
	if got := c.authRetryDelay(); got != defaultUploadAuthDelay {
		t.Fatalf("authRetryDelay = %s, want %s", got, defaultUploadAuthDelay)
	}
	if got := minDuration(time.Second, time.Minute); got != time.Second {
		t.Fatalf("minDuration = %s, want 1s", got)
	}
	c.pause(time.Minute)
	if !c.paused() {
		t.Fatal("pause should pause uploads")
	}
}
