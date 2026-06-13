package scrub

import (
	"context"
	"sync"
	"time"
)

type LatencySignal interface {
	ReadP99() time.Duration
}

type TokenBucket struct {
	mu          sync.Mutex
	bytesPerSec int64
	tokens      int64
	lastRefill  time.Time
}

func NewTokenBucket(bytesPerSec int64) *TokenBucket {
	if bytesPerSec <= 0 {
		bytesPerSec = 1
	}
	return &TokenBucket{
		bytesPerSec: bytesPerSec,
		tokens:      bytesPerSec,
		lastRefill:  time.Now(),
	}
}

func (tb *TokenBucket) Wait(ctx context.Context, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	for {
		tb.mu.Lock()
		tb.refill(bytes)
		if tb.tokens >= bytes {
			tb.tokens -= bytes
			tb.mu.Unlock()
			return nil
		}
		deficit := bytes - tb.tokens
		wait := time.Duration(float64(deficit) / float64(tb.bytesPerSec) * float64(time.Second))
		tb.mu.Unlock()

		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

func (tb *TokenBucket) refill(capacity int64) {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)
	added := int64(float64(tb.bytesPerSec) * elapsed.Seconds())
	if added > 0 {
		tb.tokens += added
		if capacity < tb.bytesPerSec {
			capacity = tb.bytesPerSec
		}
		if tb.tokens > capacity {
			tb.tokens = capacity
		}
		tb.lastRefill = now
	}
}

func ShouldPause(signal LatencySignal, threshold time.Duration) bool {
	return signal.ReadP99() > threshold
}
