package node

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/config"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestListenRequiresMTLSInProduction(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "localhost:0"
	cfg.AuthorizationPolicyPath = writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)

	server, err := Listen(cfg, Applications{})
	if server != nil {
		testutil.RequireNoErrorf(t, server.Close(), "close server")
	}
	if err == nil || !strings.Contains(err.Error(), "grpc TLS must be enabled unless local non-production storage is enabled") {
		t.Fatalf("Listen error = %v, want production TLS gate", err)
	}
}

func TestListenRejectsEnabledTLSWithoutCertificateFile(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "localhost:0"
	cfg.AuthorizationPolicyPath = writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	cfg.TLSEnabled = true
	cfg.TLSKeyFile = "server-key.pem"
	cfg.TLSCACertFile = "ca.pem"

	server, err := Listen(cfg, Applications{})
	if server != nil {
		testutil.RequireNoErrorf(t, server.Close(), "close server")
	}
	if err == nil || !strings.Contains(err.Error(), "tls_cert_file is required when grpc TLS is enabled") {
		t.Fatalf("Listen error = %v, want missing TLS certificate file", err)
	}
}

func TestListenAllowsLocalNonProductionWithoutMTLS(t *testing.T) {
	cfg := config.Default()
	cfg.PublicListenAddress = "127.0.0.1:0"
	cfg.AdminListenAddress = "localhost:0"
	cfg.AuthorizationPolicyPath = writeAuthorizationPolicy(t, `{
  "version": "policy-v1",
  "workloads": {
    "billing-etl": { "capabilities": ["public.document.head"] }
  }
}`)
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()

	server, err := Listen(cfg, Applications{})
	testutil.RequireNoErrorf(t, err, "Listen with local non-production TLS bypass")
	testutil.RequireNoErrorf(t, server.Close(), "close server")
}

func TestServerMTLSRequiresClientCertificateOnBothListeners(t *testing.T) {
	certs := writeMTLSTestFiles(t)
	cfg := config.Default()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = certs.serverCertFile
	cfg.TLSKeyFile = certs.serverKeyFile
	cfg.TLSCACertFile = certs.caCertFile
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{
		Health: fakeHealthApplication{},
	}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicWithoutCert := dialMTLSTestServer(t, publicListener, certs.clientTLSConfig(t, false))
	publicClient := scrapv1.NewDocumentServiceClient(publicWithoutCert)
	publicCtx, publicCancel := context.WithTimeout(workloadContext("test-workload"), time.Second)
	defer publicCancel()
	_, err := publicClient.HeadDocument(publicCtx, mtlsHeadRequest())
	requireCode(t, err, codes.Unavailable)
	testutil.RequireNoErrorf(t, publicWithoutCert.Close(), "close client without cert")

	publicWithCert := dialMTLSTestServer(t, publicListener, certs.clientTLSConfig(t, true))
	publicClient = scrapv1.NewDocumentServiceClient(publicWithCert)
	publicSuccessCtx, publicSuccessCancel := context.WithTimeout(workloadContext("test-workload"), time.Second)
	defer publicSuccessCancel()
	_, err = publicClient.HeadDocument(publicSuccessCtx, mtlsHeadRequest())
	requireCode(t, err, codes.Unimplemented)
	testutil.RequireNoErrorf(t, publicWithCert.Close(), "close client with cert")

	adminWithoutCert := dialMTLSTestServer(t, adminListener, certs.clientTLSConfig(t, false))
	healthClient := healthv1.NewHealthClient(adminWithoutCert)
	adminCtx, adminCancel := context.WithTimeout(context.Background(), time.Second)
	defer adminCancel()
	_, err = healthClient.Check(adminCtx, &healthv1.HealthCheckRequest{})
	requireCode(t, err, codes.Unavailable)
	testutil.RequireNoErrorf(t, adminWithoutCert.Close(), "close admin client without cert")

	adminWithCert := dialMTLSTestServer(t, adminListener, certs.clientTLSConfig(t, true))
	healthClient = healthv1.NewHealthClient(adminWithCert)
	adminSuccessCtx, adminSuccessCancel := context.WithTimeout(context.Background(), time.Second)
	defer adminSuccessCancel()
	resp, err := healthClient.Check(adminSuccessCtx, &healthv1.HealthCheckRequest{})
	testutil.RequireNoErrorf(t, err, "admin health check with client cert")
	testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_SERVING, "admin health status")
	testutil.RequireNoErrorf(t, adminWithCert.Close(), "close admin client with cert")

	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerAuthorizesCertificateIdentityWithoutMetadataHeader(t *testing.T) {
	certs := writeMTLSTestFiles(t)
	cfg := config.Default()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = certs.serverCertFile
	cfg.TLSKeyFile = certs.serverKeyFile
	cfg.TLSCACertFile = certs.caCertFile
	cfg.RequireCertificateIdentity = true
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := requireNewServerWithConfig(t, cfg, publicListener, adminListener, Applications{}, testAuthorizationManager(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { testutil.RequireNoErrorf(t, server.Close(), "close server") }()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialMTLSTestServer(t, publicListener, certs.clientTLSConfig(t, true))
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	_, err := publicClient.HeadDocument(requestCtx, mtlsHeadRequest())
	requireCode(t, err, codes.Unimplemented)

	spoofedCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		authz.WorkloadIdentityMetadataKey, "operator",
	))
	spoofedCtx, spoofedCancel := context.WithTimeout(spoofedCtx, time.Second)
	defer spoofedCancel()
	_, err = publicClient.HeadDocument(spoofedCtx, mtlsHeadRequest())
	requireCode(t, err, codes.Unimplemented)
	testutil.RequireNoErrorf(t, publicConn.Close(), "close public client")

	cancel()
	testutil.RequireNoErrorf(t, <-done, "Serve returned error")
}

func TestServerRejectsCertificateIdentityRequirementWithoutTLS(t *testing.T) {
	cfg := config.Default()
	cfg.RequireCertificateIdentity = true
	publicListener := bufconn.Listen(1024 * 1024)
	defer func() { testutil.RequireNoErrorf(t, publicListener.Close(), "close public listener") }()
	adminListener := bufconn.Listen(1024 * 1024)
	defer func() { testutil.RequireNoErrorf(t, adminListener.Close(), "close admin listener") }()

	_, err := newServerWithConfig(cfg, publicListener, adminListener, Applications{}, testAuthorizationManager(t), "")
	if err == nil || !strings.Contains(err.Error(), "require_certificate_identity requires grpc TLS to be enabled") {
		t.Fatalf("newServerWithConfig error = %v, want require-certificate TLS error", err)
	}
}

func mtlsHeadRequest() *scrapv1.HeadDocumentRequest {
	return &scrapv1.HeadDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	}
}

func dialMTLSTestServer(t *testing.T, listener *bufconn.Listener, tlsConfig *tls.Config) *grpc.ClientConn {
	t.Helper()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	)
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	return conn
}

type mtlsTestFiles struct {
	caCertFile     string
	serverCertFile string
	serverKeyFile  string
	clientCertFile string
	clientKeyFile  string
}

func (f mtlsTestFiles) clientTLSConfig(t *testing.T, includeClientCertificate bool) *tls.Config {
	t.Helper()
	caPEM, err := os.ReadFile(f.caCertFile)
	testutil.RequireNoErrorf(t, err, "read CA certificate")
	caPool := x509.NewCertPool()
	testutil.RequireTruef(t, caPool.AppendCertsFromPEM(caPEM), "append CA certificate")

	certificates := []tls.Certificate(nil)
	if includeClientCertificate {
		clientCertificate, err := tls.LoadX509KeyPair(f.clientCertFile, f.clientKeyFile)
		testutil.RequireNoErrorf(t, err, "load client certificate")
		certificates = []tls.Certificate{clientCertificate}
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      caPool,
		ServerName:   "localhost",
		Certificates: certificates,
	}
}

func writeMTLSTestFiles(t *testing.T) mtlsTestFiles {
	t.Helper()
	dir := t.TempDir()
	caCertPEM, caKey, caCert := newCertificateAuthority(t)
	serverCertPEM, serverKeyPEM := newLeafCertificate(t, caCert, caKey, 2, "scrap-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	clientCertPEM, clientKeyPEM := newLeafCertificate(t, caCert, caKey, 3, "test-workload", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	files := mtlsTestFiles{
		caCertFile:     dir + "/ca.pem",
		serverCertFile: dir + "/server.pem",
		serverKeyFile:  dir + "/server-key.pem",
		clientCertFile: dir + "/client.pem",
		clientKeyFile:  dir + "/client-key.pem",
	}
	writePEMFile(t, files.caCertFile, caCertPEM)
	writePEMFile(t, files.serverCertFile, serverCertPEM)
	writePEMFile(t, files.serverKeyFile, serverKeyPEM)
	writePEMFile(t, files.clientCertFile, clientCertPEM)
	writePEMFile(t, files.clientKeyFile, clientKeyPEM)
	return files
}

func newCertificateAuthority(t *testing.T) ([]byte, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key := newRSAKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "scrap-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	testutil.RequireNoErrorf(t, err, "create CA certificate")
	cert, err := x509.ParseCertificate(der)
	testutil.RequireNoErrorf(t, err, "parse CA certificate")
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key, cert
}

func newLeafCertificate(
	t *testing.T,
	caCert *x509.Certificate,
	caKey *rsa.PrivateKey,
	serial int64,
	commonName string,
	usages []x509.ExtKeyUsage,
) ([]byte, []byte) {
	t.Helper()
	key := newRSAKey(t)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  usages,
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	testutil.RequireNoErrorf(t, err, "create leaf certificate")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	testutil.RequireNoErrorf(t, err, "generate RSA key")
	return key
}

func writePEMFile(t *testing.T, path string, data []byte) {
	t.Helper()
	testutil.RequireNoErrorf(t, os.WriteFile(path, data, 0o600), "write PEM file %s", path)
}
