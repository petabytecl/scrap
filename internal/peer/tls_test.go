package peer_test

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestClientReplicatesWithMTLSCredentials(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "peer-a.scrap.local",
	})
	addr := startMTLSPeerServer(t, bundle)
	clientTLS := peerClientTLSConfig(t, bundle)

	client := peer.NewClient(peer.WithClientTransportCredentials(credentials.NewTLS(clientTLS)))
	defer client.Close()

	if err := replicateSmallDocument(client, addr); err != nil {
		t.Fatalf("ReplicateDocument with mTLS: %v", err)
	}
}

func TestClientCannotReplicateWithoutMTLSCredentials(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "peer-a.scrap.local",
	})
	addr := startMTLSPeerServer(t, bundle)

	client := peer.NewClient()
	defer client.Close()

	if err := replicateSmallDocument(client, addr); err == nil {
		t.Fatal("ReplicateDocument without mTLS succeeded, want handshake failure")
	}
}

func TestClientCannotReplicateWithWrongClientCA(t *testing.T) {
	serverBundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "peer-a.scrap.local",
	})
	clientBundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "peer-a.scrap.local",
	})
	addr := startMTLSPeerServer(t, serverBundle)
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFiles{
		CertPath:   clientBundle.ClientCertPath,
		KeyPath:    clientBundle.ClientKeyPath,
		RootCAPath: serverBundle.CACertPath,
		ServerName: "peer-a.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSClientConfig: %v", err)
	}

	client := peer.NewClient(peer.WithClientTransportCredentials(credentials.NewTLS(clientTLS)))
	defer client.Close()

	if err := replicateSmallDocument(client, addr); err == nil {
		t.Fatal("ReplicateDocument with wrong client CA succeeded, want handshake failure")
	}
}

func startMTLSPeerServer(t *testing.T, bundle securityfixture.CertBundle) string {
	t.Helper()
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PEER", security.TLSFiles{
		ServerCertPath: bundle.ServerCertPath,
		ServerKeyPath:  bundle.ServerKeyPath,
		ClientCAPath:   bundle.CACertPath,
		ServerName:     "peer-a.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSServerConfig: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := peer.NewServer(t.TempDir())
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	peer.RegisterServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() {
		gs.GracefulStop()
		_ = srv.Close()
	})
	return lis.Addr().String()
}

func peerClientTLSConfig(t *testing.T, bundle securityfixture.CertBundle) *tls.Config {
	t.Helper()
	clientTLS, err := security.BuildMTLSClientConfig("SCRAP_TLS_PEER", security.ClientTLSFiles{
		CertPath:   bundle.ClientCertPath,
		KeyPath:    bundle.ClientKeyPath,
		RootCAPath: bundle.CACertPath,
		ServerName: "peer-a.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSClientConfig: %v", err)
	}
	return clientTLS
}

func replicateSmallDocument(client *peer.Client, addr string) error {
	_, err := client.ReplicateDocument(context.Background(), addr, &scrapv1.ReplicateDocumentInit{
		TransactionId: "tx-mtls-001",
		DocumentName:  "doc.xml",
		ContentType:   "text/xml",
		BlockId:       1,
		TotalBytes:    4,
	}, [][]byte{[]byte("data")})
	return err
}
