package security_test

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestBuildMTLSServerConfigRequiresVerifiedClientCertificates(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "peer-a.scrap.local",
	})

	cfg, err := security.BuildMTLSServerConfig("SCRAP_TLS_PEER", security.TLSFiles{
		ServerCertPath: bundle.ServerCertPath,
		ServerKeyPath:  bundle.ServerKeyPath,
		ClientCAPath:   bundle.CACertPath,
		ServerName:     "peer-a.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSServerConfig: %v", err)
	}

	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("ClientCAs is nil")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates = %d, want 1", len(cfg.Certificates))
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want at least TLS 1.2", cfg.MinVersion)
	}
}

func TestBuildMTLSClientConfigPresentsCertificateAndVerifiesServer(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "admin.scrap.local",
	})

	cfg, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", security.ClientTLSFiles{
		CertPath:   bundle.ClientCertPath,
		KeyPath:    bundle.ClientKeyPath,
		RootCAPath: bundle.CACertPath,
		ServerName: "admin.scrap.local",
	})
	if err != nil {
		t.Fatalf("BuildMTLSClientConfig: %v", err)
	}

	if cfg.RootCAs == nil {
		t.Fatal("RootCAs is nil")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates = %d, want 1", len(cfg.Certificates))
	}
	if cfg.ServerName != "admin.scrap.local" {
		t.Fatalf("ServerName = %q, want admin.scrap.local", cfg.ServerName)
	}
}

func TestBuildMTLSClientConfigReturnsBoundedErrors(t *testing.T) {
	_, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", security.ClientTLSFiles{
		CertPath:   "/tmp/raw-client.pem",
		KeyPath:    "/tmp/raw-client-key.pem",
		RootCAPath: "/tmp/raw-ca.pem",
	})
	if err == nil {
		t.Fatal("expected missing server name error")
	}
	if !strings.Contains(err.Error(), "SCRAP_TLS_SCRAPCTL") {
		t.Fatalf("error %q does not name bounded key", err)
	}
	for _, leaked := range []string{"/tmp/raw-client.pem", "/tmp/raw-client-key.pem", "/tmp/raw-ca.pem"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error %q leaked raw path %q", err, leaked)
		}
	}
}

func TestBuildMTLSClientConfigRejectsExpiredClientCertificate(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "admin.scrap.local",
		NotBefore:  time.Now().Add(-2 * time.Hour),
		NotAfter:   time.Now().Add(-time.Hour),
	})

	_, err := security.BuildMTLSClientConfig("SCRAP_TLS_SCRAPCTL", security.ClientTLSFiles{
		CertPath:   bundle.ClientCertPath,
		KeyPath:    bundle.ClientKeyPath,
		RootCAPath: bundle.CACertPath,
		ServerName: "admin.scrap.local",
	})
	if err == nil {
		t.Fatal("expected expired client certificate error")
	}
	if !strings.Contains(err.Error(), "client certificate is outside its validity window") {
		t.Fatalf("error = %q, want expired client certificate", err)
	}
}
