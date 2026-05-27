package scrub_test

import (
	"context"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
)

func TestTokenBucket_RespectsBytesPerSecond(t *testing.T) {
	tb := scrub.NewTokenBucket(1000)
	ctx := context.Background()

	start := time.Now()
	_ = tb.Wait(ctx, 500)
	_ = tb.Wait(ctx, 500)
	_ = tb.Wait(ctx, 500)
	elapsed := time.Since(start)

	if elapsed < 400*time.Millisecond {
		t.Fatalf("expected at least 400ms for 1500 bytes at 1000 B/s, got %v", elapsed)
	}
}

func TestTokenBucket_SmallRequestImmediate(t *testing.T) {
	tb := scrub.NewTokenBucket(1_000_000)

	start := time.Now()
	_ = tb.Wait(context.Background(), 100)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Fatalf("expected near-instant for small request at high rate, got %v", elapsed)
	}
}

func TestTokenBucket_RequestLargerThanCapacityCompletes(t *testing.T) {
	tb := scrub.NewTokenBucket(1000)

	done := make(chan struct{})
	go func() {
		_ = tb.Wait(context.Background(), 1001)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Wait should complete when request is larger than bucket capacity")
	}
}

func TestLatencySignal_PausesWhenExceeded(t *testing.T) {
	signal := &stubLatencySignal{p99: 20 * time.Millisecond}

	paused := scrub.ShouldPause(signal, 10*time.Millisecond)
	if !paused {
		t.Fatal("expected pause when latency (20ms) exceeds threshold (10ms)")
	}
}

func TestLatencySignal_NoPauseWhenBelow(t *testing.T) {
	signal := &stubLatencySignal{p99: 5 * time.Millisecond}

	paused := scrub.ShouldPause(signal, 10*time.Millisecond)
	if paused {
		t.Fatal("expected no pause when latency (5ms) is below threshold (10ms)")
	}
}

func TestTokenBucket_CancelledContext(t *testing.T) {
	tb := scrub.NewTokenBucket(1)
	_ = tb.Wait(context.Background(), 1) // drain initial tokens

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := tb.Wait(ctx, 1000)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected prompt return on cancelled context, got %v", elapsed)
	}
}
