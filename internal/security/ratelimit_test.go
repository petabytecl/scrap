package security_test

import (
	"context"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/security"
)

func TestRateLimiterIsolatesSurfacesAndWindows(t *testing.T) {
	now := time.Unix(10, 0)
	observer := &recordingRateLimitObserver{}
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePublic, Limit: 2, Window: time.Minute},
			{Surface: security.RateLimitSurfacePeer, Limit: 1, Window: time.Minute},
			{Surface: security.RateLimitSurfaceAdmin, Limit: 1, Window: time.Minute},
		},
	}, security.WithRateLimitNow(func() time.Time { return now }), security.WithRateLimitObserver(observer))

	if decision := limiter.Allow(context.Background(), security.RateLimitSurfacePublic, "principal-a"); decision.Limited {
		t.Fatalf("first public decision limited: %+v", decision)
	}
	if decision := limiter.Allow(context.Background(), security.RateLimitSurfacePublic, "principal-a"); decision.Limited {
		t.Fatalf("second public decision limited: %+v", decision)
	}
	if decision := limiter.Allow(context.Background(), security.RateLimitSurfacePublic, "principal-a"); !decision.Limited {
		t.Fatalf("third public decision = %+v, want limited", decision)
	}
	if decision := limiter.Allow(context.Background(), security.RateLimitSurfacePeer, "principal-a"); decision.Limited {
		t.Fatalf("peer surface should be independent, got %+v", decision)
	}
	if observer.denials != 1 {
		t.Fatalf("observer denials = %d, want 1", observer.denials)
	}

	now = now.Add(time.Minute)
	if decision := limiter.Allow(context.Background(), security.RateLimitSurfacePublic, "principal-a"); decision.Limited {
		t.Fatalf("new public window limited: %+v", decision)
	}
}

func TestLoadRateLimitPolicyRequiresAllSecuritySurfaces(t *testing.T) {
	path := writeSecurityJSONFixture(t, map[string]any{
		"surfaces": []map[string]any{
			{"surface": "public", "limit": 10, "window": "1m"},
			{"surface": "peer", "limit": 10, "window": "1m"},
		},
	})
	if _, err := security.LoadRateLimitPolicy(path); err == nil {
		t.Fatal("LoadRateLimitPolicy succeeded without admin surface, want error")
	}
}

type recordingRateLimitObserver struct {
	denials int
}

func (o *recordingRateLimitObserver) RateLimitDenied(context.Context, security.RateLimitDecision) {
	o.denials++
}
