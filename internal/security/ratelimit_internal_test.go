package security

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiterPrunesExpiredKeys(t *testing.T) {
	now := time.Unix(10, 0)
	limiter, err := NewRateLimiter(RateLimitPolicy{
		Surfaces: []RateLimitSurfacePolicy{
			{Surface: RateLimitSurfaceAdmin, Limit: 1, Window: time.Minute},
		},
	}, WithRateLimitNow(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	limiter.Allow(context.Background(), RateLimitSurfaceAdmin, "principal-a")
	limiter.Allow(context.Background(), RateLimitSurfaceAdmin, "principal-b")

	budget := limiter.surfaces[RateLimitSurfaceAdmin]
	if len(budget.keys) != 2 {
		t.Fatalf("keys before window expiry = %d, want 2", len(budget.keys))
	}

	now = now.Add(time.Minute + time.Nanosecond)
	limiter.Allow(context.Background(), RateLimitSurfaceAdmin, "principal-c")

	if _, ok := budget.keys["principal-a"]; ok {
		t.Fatal("expired principal-a key was not pruned")
	}
	if _, ok := budget.keys["principal-b"]; ok {
		t.Fatal("expired principal-b key was not pruned")
	}
	if _, ok := budget.keys["principal-c"]; !ok {
		t.Fatal("current principal-c key missing after prune")
	}
}
