package cmd

import (
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestRunHealthcheckUsesMTLSCredentials(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "public.scrap.local",
	})
	serverTLS, err := security.BuildMTLSServerConfig("SCRAP_TLS_PUBLIC", security.TLSFiles{
		ServerCertPath: bundle.ServerCertPath,
		ServerKeyPath:  bundle.ServerKeyPath,
		ClientCAPath:   bundle.CACertPath,
		ServerName:     "public.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSServerConfig: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(gs, healthSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	err = RunHealthcheck([]string{
		"-address", lis.Addr().String(),
		"-tls-cert", bundle.ClientCertPath,
		"-tls-key", bundle.ClientKeyPath,
		"-tls-ca", bundle.CACertPath,
		"-tls-server-name", "public.scrap.local",
	})
	if err != nil {
		t.Fatalf("RunHealthcheck: %v", err)
	}
}

func TestRunHealthcheckProductionRequiresTLS(t *testing.T) {
	t.Setenv("SCRAP_SECURITY_MODE", "production")

	err := RunHealthcheck([]string{"-address", "127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected TLS error")
	}
	if !strings.Contains(err.Error(), "SCRAP_TLS_SCRAPCTL") {
		t.Fatalf("error %q does not name SCRAP_TLS_SCRAPCTL", err)
	}
}
