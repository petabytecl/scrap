package security_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"

	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
)

func TestPrincipalUnaryServerInterceptorAddsPrincipalToContext(t *testing.T) {
	authz := rolePolicyAuthorizer(t, security.RoleAdminReader)
	ctx := principalGRPCContext(t)

	interceptor := security.PrincipalUnaryServerInterceptor(authz)
	resp, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		if err := authz.Authorize(ctx, security.RoleAdminReader); err != nil {
			t.Fatalf("Authorize in handler: %v", err)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("response = %v, want ok", resp)
	}
}

func TestPrincipalStreamServerInterceptorAddsPrincipalToContext(t *testing.T) {
	authz := rolePolicyAuthorizer(t, security.RolePeerMember)
	stream := &principalServerStream{ctx: principalGRPCContext(t)}

	interceptor := security.PrincipalStreamServerInterceptor(authz)
	err := interceptor("service", stream, &grpc.StreamServerInfo{}, func(_ any, stream grpc.ServerStream) error {
		return authz.Authorize(stream.Context(), security.RolePeerMember)
	})
	if err != nil {
		t.Fatalf("stream interceptor: %v", err)
	}
}

func TestPrincipalInterceptorRejectsMissingOrNonTLSPeer(t *testing.T) {
	authz := rolePolicyAuthorizer(t, security.RoleAdminReader)
	interceptor := security.PrincipalUnaryServerInterceptor(authz)

	if _, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{}, nil); !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("missing peer = %v, want unauthenticated", err)
	}

	ctx := grpcpeer.NewContext(context.Background(), &grpcpeer.Peer{AuthInfo: fakeAuthInfo{}})
	if _, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{}, nil); !errors.Is(err, security.ErrUnauthenticated) {
		t.Fatalf("non-TLS peer = %v, want unauthenticated", err)
	}
}

func TestPrincipalInterceptorAuditsAndRateLimitsEarlyDenials(t *testing.T) {
	authz := rolePolicyAuthorizerForPrincipal(t, "spiffe://scrap/cell/cell-a/member/other/member-2", security.RoleDocumentReader)
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePublic, Limit: 1, Window: time.Minute},
		},
	})
	interceptor := security.PrincipalUnaryServerInterceptor(authz, security.WithPrincipalAudit(
		sink,
		limiter,
		security.RateLimitSurfacePublic,
		func(string) (security.GRPCAuditInfo, bool) {
			return security.GRPCAuditInfo{
				Role:      security.RoleDocumentReader,
				Operation: audit.OperationHeadDocument,
				Target:    audit.TargetDocument,
			}, true
		},
	))
	handler := func(context.Context, any) (any, error) {
		t.Fatal("handler called for denied principal")
		return "unexpected", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/scrap.v1.DocumentService/HeadDocument"}

	if _, err := interceptor(principalGRPCContext(t), "request", info, handler); !errors.Is(err, security.ErrPermissionDenied) {
		t.Fatalf("first interceptor call = %v, want permission denied", err)
	}
	if _, err := interceptor(principalGRPCContext(t), "request", info, handler); !errors.Is(err, security.ErrRateLimited) {
		t.Fatalf("second interceptor call = %v, want rate limited", err)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	assertEarlyDenialAuditEvent(t, events[0], audit.ResultDenied, audit.ReasonPermissionDenied)
	assertEarlyDenialAuditEvent(t, events[1], audit.ResultRateLimited, audit.ReasonRateLimited)
}

func assertEarlyDenialAuditEvent(t *testing.T, event audit.Event, result, reason string) {
	t.Helper()
	if event.Principal == audit.PrincipalAnonymous {
		t.Fatalf("first audit principal = anonymous, want TLS principal handle")
	}
	if event.Result != result || event.Reason != reason {
		t.Fatalf("audit event = %+v, want %s/%s", event, result, reason)
	}
	if event.Surface != audit.SurfacePublic || event.Operation != audit.OperationHeadDocument {
		t.Fatalf("unexpected audit classification: %+v", event)
	}
}

func TestPrincipalInterceptorsExemptGRPCHealthMethods(t *testing.T) {
	authz := rolePolicyAuthorizer(t, security.RoleAdminReader)
	unary := security.PrincipalUnaryServerInterceptor(authz)
	resp, err := unary(context.Background(), "request", &grpc.UnaryServerInfo{
		FullMethod: "/grpc.health.v1.Health/Check",
	}, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("health unary interceptor: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("health unary response = %v, want ok", resp)
	}

	stream := security.PrincipalStreamServerInterceptor(authz)
	err = stream("service", &principalServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{
		FullMethod: "/grpc.health.v1.Health/Watch",
	}, func(any, grpc.ServerStream) error {
		return nil
	})
	if err != nil {
		t.Fatalf("health stream interceptor: %v", err)
	}
}

func rolePolicyAuthorizer(t *testing.T, role security.Role) *security.Authorizer {
	t.Helper()
	return rolePolicyAuthorizerForPrincipal(t, principalA, role)
}

func rolePolicyAuthorizerForPrincipal(t *testing.T, principal string, role security.Role) *security.Authorizer {
	t.Helper()
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["document_writer", "document_reader", "peer_member", "admin_reader", "admin_operator", "admin_break_glass"],
		"principals": [
			{"id": "` + principal + `", "roles": ["` + string(role) + `"]}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseRolePolicy: %v", err)
	}
	return security.NewAuthorizer(policy)
}

func principalGRPCContext(t *testing.T) context.Context {
	t.Helper()
	return grpcpeer.NewContext(context.Background(), &grpcpeer.Peer{
		AuthInfo: credentials.TLSInfo{State: *verifiedTLSState(t)},
	})
}

type principalServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *principalServerStream) Context() context.Context {
	return s.ctx
}

type fakeAuthInfo struct{}

func (fakeAuthInfo) AuthType() string {
	return "fake"
}
