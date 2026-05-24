package authz

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/testutil"
)

const (
	capHead  Capability = "public.document.head"
	capWrite Capability = "public.document.write"
	capDrain Capability = "admin.member.drain.start"
)

func TestManagerAuthorizesOperationSpecificCapabilities(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})

	allowed := manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capHead)
	if !allowed.Allowed || allowed.PolicyVersion != "policy-v1" || allowed.PolicyGeneration != 1 {
		t.Fatalf("allowed decision = %#v, want policy-v1 generation 1 allow", allowed)
	}

	denied := manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capWrite)
	if denied.Allowed || denied.Reason != ReasonCapabilityDenied {
		t.Fatalf("denied decision = %#v, want capability denied", denied)
	}
}

func TestManagerRejectsMissingWorkloadIdentity(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})

	decision := manager.Authorize(context.Background(), capHead)
	if decision.Allowed || decision.Reason != ReasonMissingWorkloadIdentity {
		t.Fatalf("decision = %#v, want missing workload identity denial", decision)
	}
}

func TestWorkloadIdentityFromContextPrefersCertificateIdentity(t *testing.T) {
	ctx := ContextWithWorkloadIdentity(context.Background(), "operator")
	ctx = contextWithPeerCertificate(ctx, &x509.Certificate{
		Subject: pkix.Name{CommonName: "billing-etl"},
	})

	workload, ok := WorkloadIdentityFromContext(ctx)
	testutil.RequireTruef(t, ok, "workload identity resolved")
	testutil.RequireEqualf(t, workload, "billing-etl", "certificate workload identity")
}

func TestWorkloadIdentityFromContextUsesSPIFFEURI(t *testing.T) {
	spiffeID, err := url.Parse("spiffe://scrap.local/ns/billing/sa/etl")
	testutil.RequireNoErrorf(t, err, "parse SPIFFE ID")
	ctx := contextWithPeerCertificate(context.Background(), &x509.Certificate{
		Subject: pkix.Name{CommonName: "billing-etl"},
		URIs:    []*url.URL{spiffeID},
	})

	workload, ok := WorkloadIdentityFromContext(ctx)
	testutil.RequireTruef(t, ok, "workload identity resolved")
	testutil.RequireEqualf(t, workload, spiffeID.String(), "SPIFFE workload identity")
}

func TestWorkloadIdentityFromContextRequiresCertificateWhenConfigured(t *testing.T) {
	ctx := ContextWithWorkloadIdentity(context.Background(), "billing-etl")

	_, ok := WorkloadIdentityFromContextWithOptions(ctx, WorkloadIdentityOptions{
		RequireCertificateIdentity: true,
	})
	testutil.RequireFalsef(t, ok, "metadata identity should not satisfy certificate requirement")
}

func TestUnaryInterceptorRequiresCertificateIdentityWhenConfigured(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})
	interceptor := UnaryServerInterceptorWithOptions(
		manager,
		map[string]Capability{"/scrap.DocumentService/HeadDocument": capHead},
		InterceptorOptions{RequireCertificateIdentity: true},
	)

	_, err := interceptor(
		ContextWithWorkloadIdentity(context.Background(), "billing-etl"),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/scrap.DocumentService/HeadDocument"},
		func(context.Context, any) (any, error) {
			t.Fatal("handler ran without certificate identity")
			return nil, errors.New("unreachable")
		},
	)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %s for error %v, want Unauthenticated", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), ReasonMissingCertificateIdentity) {
		t.Fatalf("error = %v, want certificate identity reason", err)
	}
}

func TestUnaryInterceptorRejectsTenantOutsideWorkloadPolicy(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {
				Capabilities:   []Capability{capWrite},
				AllowedTenants: []string{"tenant-a"},
			},
		},
	})
	interceptor := UnaryServerInterceptorWithOptions(
		manager,
		map[string]Capability{"/scrap.DocumentService/WriteDocument": capWrite},
		InterceptorOptions{
			TenantExtractors: map[string]TenantExtractor{
				"/scrap.DocumentService/WriteDocument": tenantFromTestRequest,
			},
		},
	)

	_, err := interceptor(
		ContextWithWorkloadIdentity(context.Background(), "billing-etl"),
		testTenantRequest{tenantID: "tenant-b"},
		&grpc.UnaryServerInfo{FullMethod: "/scrap.DocumentService/WriteDocument"},
		func(context.Context, any) (any, error) {
			t.Fatal("handler ran for disallowed tenant")
			return nil, errors.New("unreachable")
		},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %s for error %v, want PermissionDenied", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), ReasonTenantDenied) {
		t.Fatalf("error = %v, want tenant denied reason", err)
	}
	if strings.Contains(err.Error(), "requires") {
		t.Fatalf("error = %v, want tenant-specific denial message", err)
	}
}

func TestStreamInterceptorRejectsTenantOutsideWorkloadPolicy(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {
				Capabilities:   []Capability{capWrite},
				AllowedTenants: []string{"tenant-a"},
			},
		},
	})
	interceptor := StreamServerInterceptorWithOptions(
		manager,
		map[string]Capability{"/scrap.DocumentService/WriteDocument": capWrite},
		InterceptorOptions{
			TenantExtractors: map[string]TenantExtractor{
				"/scrap.DocumentService/WriteDocument": tenantFromTestRequest,
			},
		},
	)

	err := interceptor(
		nil,
		&testServerStream{
			ctx:      ContextWithWorkloadIdentity(context.Background(), "billing-etl"),
			messages: []testTenantRequest{{tenantID: "tenant-b"}},
		},
		&grpc.StreamServerInfo{FullMethod: "/scrap.DocumentService/WriteDocument"},
		func(_ any, stream grpc.ServerStream) error {
			var req testTenantRequest
			return stream.RecvMsg(&req)
		},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %s for error %v, want PermissionDenied", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), ReasonTenantDenied) {
		t.Fatalf("error = %v, want tenant denied reason", err)
	}
	if strings.Contains(err.Error(), "requires") {
		t.Fatalf("error = %v, want tenant-specific denial message", err)
	}
}

func TestStreamInterceptorNormalizesTenantBeforeAuthorization(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {
				Capabilities:   []Capability{capWrite},
				AllowedTenants: []string{"tenant-a"},
			},
		},
	})
	interceptor := StreamServerInterceptorWithOptions(
		manager,
		map[string]Capability{"/scrap.DocumentService/WriteDocument": capWrite},
		InterceptorOptions{
			TenantExtractors: map[string]TenantExtractor{
				"/scrap.DocumentService/WriteDocument": tenantFromTestRequest,
			},
		},
	)

	err := interceptor(
		nil,
		&testServerStream{
			ctx:      ContextWithWorkloadIdentity(context.Background(), "billing-etl"),
			messages: []testTenantRequest{{tenantID: "tenant-a "}},
		},
		&grpc.StreamServerInfo{FullMethod: "/scrap.DocumentService/WriteDocument"},
		func(_ any, stream grpc.ServerStream) error {
			var req testTenantRequest
			return stream.RecvMsg(&req)
		},
	)
	testutil.RequireNoErrorf(t, err, "stream tenant authorization")
}

func TestManagerAllowsAnyTenantWhenAllowedTenantsMissing(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"operator": {Capabilities: []Capability{capDrain}},
		},
	})

	decision := manager.AuthorizeTenant(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain, "tenant-b")
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want unrestricted tenant access", decision)
	}
}

func TestManagerAllowsAnyTenantWhenAllowedTenantsEmpty(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"operator": {
				Capabilities:   []Capability{capDrain},
				AllowedTenants: []string{},
			},
		},
	})

	decision := manager.AuthorizeTenant(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain, "tenant-b")
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want unrestricted tenant access", decision)
	}
}

func TestUnaryInterceptorKeepsAuthorizationErrorWhenDeniedAuditFails(t *testing.T) {
	manager := newTestManager(t, Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"billing-etl": {Capabilities: []Capability{capHead}},
		},
	})
	auditSink := failingDeniedAuditSink{err: errors.New("audit store unavailable")}
	interceptor := UnaryServerInterceptor(
		manager,
		map[string]Capability{"/scrap.DocumentService/WriteDocument": capWrite},
		auditSink,
	)

	_, err := interceptor(
		ContextWithWorkloadIdentity(context.Background(), "billing-etl"),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/scrap.DocumentService/WriteDocument"},
		func(context.Context, any) (any, error) {
			t.Fatal("handler ran for denied request")
			return nil, errors.New("unreachable")
		},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %s for error %v, want PermissionDenied", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), ReasonCapabilityDenied) {
		t.Fatalf("error = %v, want original authorization reason", err)
	}
}

func TestInvalidPolicyFailsClosed(t *testing.T) {
	_, err := NewManager(Policy{
		Version: "policy-v1",
		Workloads: map[string]WorkloadPolicy{
			"operator": {Capabilities: []Capability{"admin"}},
		},
	}, []Capability{capHead})
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("error = %v, want invalid policy", err)
	}
}

func TestLoadFileRejectsTrailingJSON(t *testing.T) {
	path := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}
{
  "version": "policy-v2",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.write"] }
  }
}`)
	_, err := LoadFile(path)
	if !errors.Is(err, ErrInvalidPolicy) || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("error = %v, want trailing JSON invalid policy", err)
	}
}

func TestBadReloadKeepsLastValidPolicyAndAlerts(t *testing.T) {
	validPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(validPath, []Capability{capDrain})
	testutil.RequireNoErrorf(t, err, "load manager")
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("initial policy did not allow operator drain")
	}

	badPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "operator": { "capabilities": ["admin"] }
  }
}`)
	if err := manager.ReloadFile(badPath); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("reload error = %v, want invalid policy", err)
	}
	if manager.PolicyVersion() != "policy-v1" || manager.Generation() != 1 {
		t.Fatalf("active policy = version %s generation %d, want original policy", manager.PolicyVersion(), manager.Generation())
	}
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("last valid policy was not preserved after bad reload")
	}
	alerts := manager.ReloadAlerts()
	if len(alerts) != 1 || alerts[0].Code != ReasonPolicyReloadRejected || !strings.Contains(alerts[0].Message, "admin") {
		t.Fatalf("alerts = %#v, want rejected policy alert", alerts)
	}
}

func TestReloadAlertsAreBounded(t *testing.T) {
	validPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(validPath, []Capability{capDrain})
	testutil.RequireNoErrorf(t, err, "load manager")
	badPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "operator": { "capabilities": ["admin"] }
  }
}`)

	for range maxReloadAlerts + 5 {
		if err := manager.ReloadFile(badPath); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("reload error = %v, want invalid policy", err)
		}
	}
	alerts := manager.ReloadAlerts()
	if len(alerts) != maxReloadAlerts {
		t.Fatalf("alert count = %d, want %d", len(alerts), maxReloadAlerts)
	}
}

func TestGoodReloadReplacesPolicy(t *testing.T) {
	firstPath := writePolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "operator": { "capabilities": ["admin.member.drain.start"] }
  }
}`)
	manager, err := LoadManagerFromFile(firstPath, []Capability{capDrain, capHead})
	testutil.RequireNoErrorf(t, err, "load manager")
	secondPath := writePolicy(t, `{
  "version": "policy-v2",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	testutil.RequireNoErrorf(t, manager.ReloadFile(secondPath), "reload valid policy")
	if manager.PolicyVersion() != "policy-v2" || manager.Generation() != 2 {
		t.Fatalf("active policy = version %s generation %d, want policy-v2 generation 2", manager.PolicyVersion(), manager.Generation())
	}
	if manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "operator"), capDrain).Allowed {
		t.Fatal("old operator capability remained after reload")
	}
	if !manager.Authorize(ContextWithWorkloadIdentity(context.Background(), "billing-etl"), capHead).Allowed {
		t.Fatal("new policy did not allow billing-etl head")
	}
}

func contextWithPeerCertificate(ctx context.Context, cert *x509.Certificate) context.Context {
	return grpcpeer.NewContext(ctx, &grpcpeer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{cert},
			},
		},
	})
}

func newTestManager(t *testing.T, policy Policy) *Manager {
	t.Helper()
	manager, err := NewManager(policy, []Capability{capHead, capWrite, capDrain})
	testutil.RequireNoErrorf(t, err, "new manager")
	return manager
}

type failingDeniedAuditSink struct {
	err error
}

func (s failingDeniedAuditSink) RecordDeniedRequest(context.Context, string, Decision) error {
	return s.err
}

type testTenantRequest struct {
	tenantID string
}

func tenantFromTestRequest(req any) (string, bool) {
	request, ok := req.(testTenantRequest)
	if !ok {
		requestPointer, pointerOK := req.(*testTenantRequest)
		if !pointerOK || requestPointer == nil {
			return "", false
		}
		request = *requestPointer
	}
	if request.tenantID == "" {
		return "", false
	}
	return request.tenantID, true
}

type testServerStream struct {
	grpc.ServerStream
	ctx      context.Context
	messages []testTenantRequest
}

func (s *testServerStream) Context() context.Context {
	return s.ctx
}

func (s *testServerStream) RecvMsg(req any) error {
	if len(s.messages) == 0 {
		return io.EOF
	}
	target, ok := req.(*testTenantRequest)
	if !ok {
		return errors.New("expected *testTenantRequest")
	}
	*target = s.messages[0]
	s.messages = s.messages[1:]
	return nil
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "policy-*.json")
	testutil.RequireNoErrorf(t, err, "create policy")
	if _, err := file.WriteString(body); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close policy")
	return file.Name()
}
