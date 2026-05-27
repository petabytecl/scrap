package shard

import (
	"context"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
)

func TestUploadThrottleReducesAndSuccessesRestoreConcurrency(t *testing.T) {
	s := &Shard{upload: UploadConfig{Concurrency: 3}}
	s.resetUploadConcurrency()

	s.recordUploadThrottle()
	if got := s.uploadConcurrency(); got != 2 {
		t.Fatalf("concurrency after first throttle = %d, want 2", got)
	}
	s.recordUploadThrottle()
	if got := s.uploadConcurrency(); got != 1 {
		t.Fatalf("concurrency after second throttle = %d, want 1", got)
	}
	s.recordUploadThrottle()
	if got := s.uploadConcurrency(); got != 1 {
		t.Fatalf("concurrency floor after third throttle = %d, want 1", got)
	}

	for range successesToRestoreUpload {
		s.recordUploadSuccess()
	}
	if got := s.uploadConcurrency(); got != 2 {
		t.Fatalf("concurrency after five successes = %d, want 2", got)
	}
}

func TestUploadRequeueMovesFailedBlocksToBack(t *testing.T) {
	s := &Shard{uploadRequeued: make(map[uint64]struct{})}
	s.markUploadRequeued(1)

	ordered := s.orderPendingUploads([]PendingUpload{
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
	s := newRetryTestShard()
	s.resetUploadConcurrency()

	throttled := newUploadRetryState(time.Nanosecond)
	retry, err := s.handleUploadRetry(ctx, backend.ErrThrottled, &throttled)
	if err != nil || !retry {
		t.Fatalf("throttled retry = %v, err = %v; want retry without error", retry, err)
	}
	if got := s.uploadConcurrency(); got != 1 {
		t.Fatalf("throttled concurrency = %d, want 1", got)
	}
}

func TestHandleUploadRetryTransient(t *testing.T) {
	ctx := context.Background()
	s := newRetryTestShard()

	transient := newUploadRetryState(time.Nanosecond)
	retry, err := s.handleUploadRetry(ctx, backend.ErrTransient, &transient)
	if err != nil || !retry {
		t.Fatalf("transient retry = %v, err = %v; want retry without error", retry, err)
	}
	transient.transientAttempts = maxTransientUploadRetries
	retry, err = s.handleUploadRetry(ctx, backend.ErrTransient, &transient)
	if err != nil || retry {
		t.Fatalf("exhausted transient retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func TestHandleUploadRetryCorrupt(t *testing.T) {
	ctx := context.Background()
	s := newRetryTestShard()

	corrupt := newUploadRetryState(time.Nanosecond)
	retry, err := s.handleUploadRetry(ctx, backend.ErrCorrupt, &corrupt)
	if err != nil || !retry {
		t.Fatalf("first corrupt retry = %v, err = %v; want retry without error", retry, err)
	}
	retry, err = s.handleUploadRetry(ctx, backend.ErrCorrupt, &corrupt)
	if err != nil || retry {
		t.Fatalf("second corrupt retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func TestHandleUploadRetryAuthAndPermanent(t *testing.T) {
	ctx := context.Background()
	s := newRetryTestShard()
	state := newUploadRetryState(time.Nanosecond)

	retry, err := s.handleUploadRetry(ctx, backend.ErrAuth, &state)
	if err != nil || retry {
		t.Fatalf("auth retry = %v, err = %v; want pause without retry error", retry, err)
	}

	retry, err = s.handleUploadRetry(ctx, backend.ErrPermanent, &state)
	if err != nil || retry {
		t.Fatalf("permanent retry = %v, err = %v; want no retry without error", retry, err)
	}
}

func newRetryTestShard() *Shard {
	return &Shard{upload: UploadConfig{
		Concurrency:    2,
		RetryBaseDelay: time.Nanosecond,
		AuthRetryDelay: time.Nanosecond,
	}}
}

func TestUploadConfigDefaults(t *testing.T) {
	s := &Shard{}

	if got := s.configuredUploadConcurrency(); got != DefaultUploadConcurrency {
		t.Fatalf("configuredUploadConcurrency = %d, want %d", got, DefaultUploadConcurrency)
	}
	if got := s.uploadCellID(); got != "local" {
		t.Fatalf("uploadCellID = %q, want local", got)
	}
	if got := s.uploadRetryBaseDelay(); got != defaultUploadRetryBase {
		t.Fatalf("uploadRetryBaseDelay = %s, want %s", got, defaultUploadRetryBase)
	}
	if got := s.uploadAuthRetryDelay(); got != defaultUploadAuthDelay {
		t.Fatalf("uploadAuthRetryDelay = %s, want %s", got, defaultUploadAuthDelay)
	}
	if got := minDuration(time.Second, time.Minute); got != time.Second {
		t.Fatalf("minDuration = %s, want 1s", got)
	}
	s.pauseUploads(time.Minute)
	if !s.uploadPaused() {
		t.Fatal("pauseUploads should pause uploads")
	}
}
