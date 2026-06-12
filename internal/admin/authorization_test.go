package admin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/quarantine"
	"github.com/petabytecl/scrap/internal/security"
)

func TestAdminAuthorizationDeniesOperatorEndpointBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	applier := &recordingEvictionApplier{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithEvictionApplier(applier))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodPost, "/admin/eviction/plans/plan-1/apply", bytes.NewReader([]byte(`{}`)))
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if applier.calls != 0 {
		t.Fatalf("applier calls = %d, want 0", applier.calls)
	}
}

func TestAdminAuthorizationDeniesPlannerBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	planner := &recordingEvictionPlanner{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithEvictionPlanner(planner))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodPost, "/admin/eviction/plans", bytes.NewReader([]byte(`{}`)))
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if planner.calls != 0 {
		t.Fatalf("planner calls = %d, want 0", planner.calls)
	}
}

func TestAdminAuthorizationDeniesRewrapBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	service := &rewrapServiceStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithRewrapService(service))

	req := httptest.NewRequestWithContext(
		adminAuthContext(security.RoleAdminReader),
		http.MethodPost,
		"/admin/rewrap/document",
		bytes.NewReader([]byte(`{"transaction_id":"tx","document_name":"doc.xml"}`)),
	)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if service.calls != 0 {
		t.Fatalf("rewrap calls = %d, want 0", service.calls)
	}
}

func TestAdminAuthorizationDeniesQuarantineConfirmBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	service := &quarantineServiceStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithQuarantineService(service))

	req := httptest.NewRequestWithContext(
		adminAuthContext(security.RoleAdminReader),
		http.MethodPost,
		"/admin/quarantine/confirm",
		bytes.NewReader([]byte(`{"transaction_id":"tx","document_name":"doc.xml"}`)),
	)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if service.confirmCalls != 0 {
		t.Fatalf("confirm calls = %d, want 0", service.confirmCalls)
	}
}

func TestAdminAuthorizationAllowsQuarantineReleaseForBreakGlass(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	service := &quarantineServiceStub{
		result: quarantine.Result{Status: quarantine.StatusOK, Reason: quarantine.ReasonOK, Changed: true},
	}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithQuarantineService(service))

	req := httptest.NewRequestWithContext(
		adminAuthContext(security.RoleAdminBreakGlass),
		http.MethodPost,
		"/admin/quarantine/release",
		bytes.NewReader([]byte(`{"transaction_id":"tx","document_name":"doc.xml"}`)),
	)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if service.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", service.releaseCalls)
	}
}

func TestAdminAuthorizationAllowsReaderStatusEndpoint(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	status := &recordingEvictionStatusProvider{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithEvictionPlanStatusProvider(status))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodGet, "/admin/eviction/plans/plan-1", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if status.calls != 1 {
		t.Fatalf("status provider calls = %d, want 1", status.calls)
	}
}

func TestAdminAuthorizationDeniesMetricsBeforeHandler(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	calls := 0
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithMetrics(metrics))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if calls != 0 {
		t.Fatalf("metrics calls = %d, want 0", calls)
	}
}

func TestAdminAuthorizationDeniesBreakGlassEndpointsBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	injector := &projectionInjectorStub{}
	rotator := &transitRotatorStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithProjectionInjector(injector), admin.WithTransitRotator(rotator), admin.WithPprof())

	hookReq := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodPost, "/test-hooks/projection-key", bytes.NewReader([]byte(`{}`)))
	hookResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(hookResp, hookReq)

	if hookResp.Code != http.StatusForbidden {
		t.Fatalf("hook status = %d, want 403", hookResp.Code)
	}
	if injector.txID != "" || injector.blockID != 0 || injector.docCount != 0 || injector.completed {
		t.Fatalf("projection injector was called: %+v", injector)
	}

	rotateReq := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodPost, "/test-hooks/transit-rotate", nil)
	rotateResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rotateResp, rotateReq)

	if rotateResp.Code != http.StatusForbidden {
		t.Fatalf("rotate status = %d, want 403", rotateResp.Code)
	}
	if rotator.calls != 0 {
		t.Fatalf("transit rotate calls = %d, want 0", rotator.calls)
	}

	profileReq := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodGet, "/debug/pprof/profile", nil)
	profileResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(profileResp, profileReq)

	if profileResp.Code != http.StatusForbidden {
		t.Fatalf("profile status = %d, want 403", profileResp.Code)
	}
}

func TestAdminAuthorizationDeniesLightScrubHookBeforeSideEffect(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	scrubber := &lightScrubberStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithLightScrubber(scrubber))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodPost, "/test-hooks/light-scrub", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	if scrubber.calls != 0 {
		t.Fatalf("light scrub calls = %d, want 0", scrubber.calls)
	}
}

func TestAdminAuthorizationAllowsTransitRotateHookForBreakGlass(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	rotator := &transitRotatorStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithTransitRotator(rotator))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminBreakGlass), http.MethodPost, "/test-hooks/transit-rotate", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.Code)
	}
	if rotator.calls != 1 {
		t.Fatalf("transit rotate calls = %d, want 1", rotator.calls)
	}
}

func TestAdminAuthorizationAllowsLightScrubHookForBreakGlass(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	scrubber := &lightScrubberStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithLightScrubber(scrubber))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminBreakGlass), http.MethodPost, "/test-hooks/light-scrub", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.Code)
	}
	if scrubber.calls != 1 {
		t.Fatalf("light scrub calls = %d, want 1", scrubber.calls)
	}
}

func TestAdminAuthorizationAllowsReaderEndpoint(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	srv := admin.New(admin.WithAuthorizer(authz))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
}

func TestAdminHealthReportsBoundedAuthorizationStatus(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	authz.RecordAuthorizationStatus(security.AuthorizationStatusMismatch)
	srv := admin.New(admin.WithAuthorizer(authz))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminReader), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()

	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), `"authorization_status":"mismatch"`) {
		t.Fatalf("health body missing bounded authorization status: %s", resp.Body.String())
	}
}

func adminAuthContext(roles ...security.Role) context.Context {
	return security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "admin",
		Roles: security.NewRoleSet(roles...),
	})
}

type recordingEvictionPlanner struct {
	calls int
}

func (p *recordingEvictionPlanner) CreateEvictionPlan(context.Context, eviction.PlanRequest) (eviction.Plan, error) {
	p.calls++
	return eviction.Plan{PlanID: "plan-1"}, nil
}

type recordingEvictionApplier struct {
	calls int
}

func (a *recordingEvictionApplier) ApplyEvictionPlan(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
	a.calls++
	return eviction.ApplyResult{}, errors.New("should not be called")
}

type recordingEvictionStatusProvider struct {
	calls int
}

func (p *recordingEvictionStatusProvider) EvictionPlanStatus(context.Context, string) (eviction.PlanStatus, error) {
	p.calls++
	return eviction.PlanStatus{PlanID: "plan-1", Status: eviction.PlanStatusPending}, nil
}
