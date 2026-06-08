package security_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcpeer "google.golang.org/grpc/peer"

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
