package security_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"

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

func rolePolicyAuthorizer(t *testing.T, role security.Role) *security.Authorizer {
	t.Helper()
	policy, err := security.ParseRolePolicy([]byte(`{
		"roles": ["document_writer", "document_reader", "peer_member", "admin_reader", "admin_operator", "admin_break_glass"],
		"principals": [
			{"id": "spiffe://scrap/cell/cell-a/member/member-a/member-1", "roles": ["` + string(role) + `"]}
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
