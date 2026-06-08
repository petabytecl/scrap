package security_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestExtractPeerIdentityFromVerifiedCertificateURI(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: "spiffe://scrap/cell/cell-a/member/member-a/member-1",
	})

	identity, err := security.ExtractPeerIdentity(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
		VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
	})
	if err != nil {
		t.Fatalf("ExtractPeerIdentity: %v", err)
	}

	if identity != (security.PeerIdentityConfig{CellID: "cell-a", MemberHostname: "member-a", MemberID: "member-1"}) {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestExtractPeerIdentityRejectsMalformedURI(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ClientURI: "spiffe://scrap/cell/cell-a/member/member-a",
	})

	_, err := security.ExtractPeerIdentity(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{bundle.ClientCertificate},
		VerifiedChains:   [][]*x509.Certificate{{bundle.ClientCertificate}},
	})
	if err == nil {
		t.Fatal("expected malformed URI error")
	}
	if strings.Contains(err.Error(), "cell-a") || strings.Contains(err.Error(), "member-a") {
		t.Fatalf("error %q leaked unbounded identity values", err)
	}
}

func TestPeerIdentityContextRoundTrip(t *testing.T) {
	want := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "member-a",
		MemberID:       "member-1",
	}

	ctx := security.ContextWithPeerIdentity(context.Background(), want)
	got, ok := security.PeerIdentityFromContext(ctx)
	if !ok {
		t.Fatal("identity missing from context")
	}
	if got != want {
		t.Fatalf("identity = %#v, want %#v", got, want)
	}
}

func TestPeerIdentityPrincipalID(t *testing.T) {
	identity := security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "member-a",
		MemberID:       "member-1",
	}
	got := security.PeerIdentityPrincipalID(identity)
	if got != "spiffe://scrap/cell/cell-a/member/member-a/member-1" {
		t.Fatalf("PeerIdentityPrincipalID = %q", got)
	}

	identity.MemberID = "bad member"
	if got := security.PeerIdentityPrincipalID(identity); got != "" {
		t.Fatalf("PeerIdentityPrincipalID invalid = %q, want empty", got)
	}
}
