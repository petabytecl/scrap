package admin_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/security"
)

func TestAdminAuditsDangerousOperationAndRateLimitDenial(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfaceAdmin, Limit: 1, Window: time.Minute},
		},
	})
	applier := &successfulEvictionApplier{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithRateLimiter(limiter), admin.WithEvictionApplier(applier))
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminOperator),
	})

	first := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/eviction/plans/plan-1/apply", bytes.NewReader([]byte(`{}`)))
	firstResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstResp, first)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200: %s", firstResp.Code, firstResp.Body.String())
	}

	second := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/eviction/plans/plan-1/apply", bytes.NewReader([]byte(`{}`)))
	secondResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondResp, second)
	if secondResp.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429: %s", secondResp.Code, secondResp.Body.String())
	}
	if applier.calls != 1 {
		t.Fatalf("applier calls = %d, want 1", applier.calls)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationEvictionApply || events[0].Result != audit.ResultAllowed {
		t.Fatalf("first audit event = %+v", events[0])
	}
	if events[1].Result != audit.ResultRateLimited || events[1].Reason != audit.ReasonRateLimited {
		t.Fatalf("second audit event = %+v", events[1])
	}
}

func TestAdminAuditsDeniedDangerousOperation(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	applier := &successfulEvictionApplier{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithEvictionApplier(applier))
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminReader),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/eviction/plans/plan-1/apply", bytes.NewReader([]byte(`{}`)))
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body.String())
	}
	if applier.calls != 0 {
		t.Fatalf("applier calls = %d, want 0", applier.calls)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationEvictionApply || events[0].Result != audit.ResultDenied {
		t.Fatalf("audit event = %+v", events[0])
	}
}

func TestAdminAuditsWildcardPprofRoutesAsProfiles(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithPprof())
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminBreakGlass),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/debug/pprof/goroutine?debug=1", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationPprofProfile || events[0].Target != audit.TargetProfile {
		t.Fatalf("audit event = %+v, want pprof_profile/profile", events[0])
	}
}

type successfulEvictionApplier struct {
	calls int
}

func (a *successfulEvictionApplier) ApplyEvictionPlan(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
	a.calls++
	return eviction.ApplyResult{PlanID: "plan-1"}, nil
}
