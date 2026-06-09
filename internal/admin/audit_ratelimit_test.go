package admin_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
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

func TestAdminAuditsMethodDeniedDangerousOperation(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	injector := &projectionInjectorStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithProjectionInjector(injector))
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminBreakGlass),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/test-hooks/projection-key", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405: %s", resp.Code, resp.Body.String())
	}
	if injector.txID != "" || injector.blockID != 0 || injector.docCount != 0 || injector.completed {
		t.Fatalf("projection injector was called: %+v", injector)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationProjectionKeyHook || events[0].Result != audit.ResultDenied || events[0].Reason != audit.ReasonMethodNotAllowed {
		t.Fatalf("audit event = %+v, want projection_key_hook denied method_not_allowed", events[0])
	}
}

func TestAdminAuditsEvictionPlanCollectionMethodDenialAsCreate(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	planner := &recordingEvictionPlanner{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithEvictionPlanner(planner))
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminOperator),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/eviction/plans", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405: %s", resp.Code, resp.Body.String())
	}
	if planner.calls != 0 {
		t.Fatalf("planner calls = %d, want 0", planner.calls)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationEvictionPlanCreate ||
		events[0].Target != audit.TargetBlock ||
		events[0].Result != audit.ResultDenied ||
		events[0].Reason != audit.ReasonMethodNotAllowed {
		t.Fatalf("audit event = %+v, want eviction_plan_create/block denied method_not_allowed", events[0])
	}
}

func TestAdminAuditsDistinctDeniedTLSPrincipals(t *testing.T) {
	authz := adminAuthorizerForPrincipal(t, "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a", security.RoleAdminReader)
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfaceAdmin, Limit: 1, Window: time.Minute},
		},
	})
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithRateLimiter(limiter))

	unknownPrincipals := []string{
		"spiffe://scrap/cell/cell-a/member/scrapd-0/member-unknown-a",
		"spiffe://scrap/cell/cell-a/member/scrapd-0/member-unknown-b",
	}
	for _, principal := range unknownPrincipals {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		req.TLS = adminTLSStateForPrincipal(t, principal)
		resp := httptest.NewRecorder()
		srv.Handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("status for %s = %d, want 403: %s", principal, resp.Code, resp.Body.String())
		}
	}

	events := sink.Events()
	if len(events) != len(unknownPrincipals) {
		t.Fatalf("audit events = %d, want %d: %+v", len(events), len(unknownPrincipals), events)
	}
	for i, principal := range unknownPrincipals {
		if events[i].Principal != audit.PrincipalHandle(principal) {
			t.Fatalf("event %d principal = %q, want %q", i, events[i].Principal, audit.PrincipalHandle(principal))
		}
		if events[i].Result != audit.ResultDenied || events[i].Reason != audit.ReasonPermissionDenied {
			t.Fatalf("event %d = %+v, want denied permission_denied", i, events[i])
		}
	}
}

func TestAdminAuditsRewrapRouteAsDocumentOperation(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	service := &rewrapServiceStub{}
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithRewrapService(service))
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminOperator),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/rewrap/document", bytes.NewReader([]byte(`{"transaction_id":"tx","document_name":"doc.xml"}`)))
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationRewrapDocument || events[0].Target != audit.TargetDocument || events[0].Result != audit.ResultAllowed {
		t.Fatalf("audit event = %+v, want rewrap_document/document allowed", events[0])
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

func TestAdminAuditsRejectedPprofMethodsAsDenials(t *testing.T) {
	authz := security.NewStaticAuthorizer()
	sink := audit.NewMemorySink()
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithAuditSink(sink), admin.WithPprof())
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
		Roles: security.NewRoleSet(security.RoleAdminBreakGlass),
	})

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/debug/pprof/profile", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405: %s", resp.Code, resp.Body.String())
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].Operation != audit.OperationPprofProfile || events[0].Result != audit.ResultDenied || events[0].Reason != audit.ReasonMethodNotAllowed {
		t.Fatalf("audit event = %+v, want pprof_profile denied method_not_allowed", events[0])
	}
}

type successfulEvictionApplier struct {
	calls int
}

func (a *successfulEvictionApplier) ApplyEvictionPlan(context.Context, eviction.ApplyRequest) (eviction.ApplyResult, error) {
	a.calls++
	return eviction.ApplyResult{PlanID: "plan-1"}, nil
}

func adminAuthorizerForPrincipal(t *testing.T, principal string, role security.Role) *security.Authorizer {
	t.Helper()
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["` + string(role) + `"],
		"principals": [
			{"id": "` + principal + `", "roles": ["` + string(role) + `"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}
	return security.NewAuthorizer(policy)
}

func adminTLSStateForPrincipal(t *testing.T, principal string) *tls.ConnectionState {
	t.Helper()
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: principal,
	})
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
		VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
	}
}
