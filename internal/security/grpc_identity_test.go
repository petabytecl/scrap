package security_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/audit"
	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestPeerIdentityUnaryServerInterceptorAddsIdentityToContext(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: "spiffe://scrap/cell/cell-a/member/member-a/member-1",
	})
	ctx := grpcpeer.NewContext(context.Background(), &grpcpeer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
			VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
		}},
	})

	interceptor := security.PeerIdentityUnaryServerInterceptor()
	_, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		identity, ok := security.PeerIdentityFromContext(ctx)
		if !ok {
			t.Fatal("identity missing from context")
		}
		if identity.MemberID != "member-1" {
			t.Fatalf("MemberID = %q, want member-1", identity.MemberID)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
}

func TestPeerIdentityInterceptorAuditsAndRateLimitsIdentityDenials(t *testing.T) {
	ctx := grpcContextWithClientURI(t, "spiffe://scrap/service/non-peer")
	sink := audit.NewMemorySink()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfacePeer, Limit: 1, Window: time.Minute},
		},
	})
	interceptor := security.PeerIdentityUnaryServerInterceptor(security.WithPrincipalAudit(
		sink,
		limiter,
		security.RateLimitSurfacePeer,
		func(string) (security.GRPCAuditInfo, bool) {
			return security.GRPCAuditInfo{
				Role:      security.RolePeerMember,
				Operation: audit.OperationForwardRaft,
				Target:    audit.TargetPeer,
			}, true
		},
	))
	handler := func(context.Context, any) (any, error) {
		t.Fatal("handler called for missing peer identity")
		return "unexpected", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/scrap.v1.PeerService/ForwardRaft"}

	if _, err := interceptor(ctx, "request", info, handler); !errors.Is(err, security.ErrUnauthenticated) || status.Code(err) != codes.Unauthenticated {
		t.Fatalf("first interceptor call = %v (%s), want unauthenticated", err, status.Code(err))
	}
	if _, err := interceptor(ctx, "request", info, handler); !errors.Is(err, security.ErrRateLimited) || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second interceptor call = %v (%s), want rate limited", err, status.Code(err))
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2: %+v", len(events), events)
	}
	if events[0].Result != audit.ResultDenied || events[0].Reason != audit.ReasonUnauthenticated {
		t.Fatalf("first audit event = %+v, want unauthenticated denial", events[0])
	}
	if events[1].Result != audit.ResultRateLimited || events[1].Reason != audit.ReasonRateLimited {
		t.Fatalf("second audit event = %+v, want rate limit denial", events[1])
	}
}

func grpcContextWithClientURI(t *testing.T, uri string) context.Context {
	t.Helper()
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: uri,
	})
	return grpcpeer.NewContext(context.Background(), &grpcpeer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
			VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
		}},
	})
}
