// Package securityfixture creates ephemeral TLS material for tests.
package securityfixture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const serialBits = 128

type CertBundle struct {
	CACertPath     string
	ServerCertPath string
	ServerKeyPath  string
	ClientCertPath string
	ClientKeyPath  string

	ServerCertificate *x509.Certificate
	ClientCertificate *x509.Certificate
}

type CertOptions struct {
	ServerName string
	ClientURI  string
	NotBefore  time.Time
	NotAfter   time.Time
}

func WriteCertBundle(t *testing.T, dir string, opts CertOptions) CertBundle {
	t.Helper()
	opts = certOptionsWithDefaults(opts)

	ca := createCA(t, opts)
	server := createServerCertificate(t, opts, ca.cert, ca.key)
	client := createClientCertificate(t, opts, ca.cert, ca.key)

	return CertBundle{
		CACertPath:        writeCert(t, dir, "ca.pem", ca.der),
		ServerCertPath:    writeCert(t, dir, "server.pem", server.der),
		ServerKeyPath:     writeKey(t, dir, "server-key.pem", server.key),
		ClientCertPath:    writeCert(t, dir, "client.pem", client.der),
		ClientKeyPath:     writeKey(t, dir, "client-key.pem", client.key),
		ServerCertificate: server.cert,
		ClientCertificate: client.cert,
	}
}

type certMaterial struct {
	der  []byte
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func certOptionsWithDefaults(opts CertOptions) CertOptions {
	if opts.ServerName == "" {
		opts.ServerName = "scrap.local"
	}
	if opts.ClientURI == "" {
		opts.ClientURI = "spiffe://scrap/cell/cell-a/member/member-a/member-1"
	}
	if opts.NotBefore.IsZero() {
		opts.NotBefore = time.Now().Add(-time.Hour)
	}
	if opts.NotAfter.IsZero() {
		opts.NotAfter = time.Now().Add(time.Hour)
	}
	return opts
}

func createCA(t *testing.T, opts CertOptions) certMaterial {
	t.Helper()
	key := newKey(t)
	template := certificateTemplate(t, "scrap test CA", opts.NotBefore, opts.NotAfter)
	template.IsCA = true
	template.BasicConstraintsValid = true
	template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	return signCertificate(t, "CA", template, template, key, key)
}

func createServerCertificate(t *testing.T, opts CertOptions, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) certMaterial {
	t.Helper()
	key := newKey(t)
	template := certificateTemplate(t, "scrap server", opts.NotBefore, opts.NotAfter)
	template.KeyUsage = x509.KeyUsageDigitalSignature
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if ip := net.ParseIP(opts.ServerName); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{opts.ServerName}
	}
	return signCertificate(t, "server", template, caCert, key, caKey)
}

func createClientCertificate(t *testing.T, opts CertOptions, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) certMaterial {
	t.Helper()
	key := newKey(t)
	template := certificateTemplate(t, "scrap client", opts.NotBefore, opts.NotAfter)
	template.KeyUsage = x509.KeyUsageDigitalSignature
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	clientURI, err := url.Parse(opts.ClientURI)
	if err != nil {
		t.Fatalf("parse client URI: %v", err)
	}
	template.URIs = []*url.URL{clientURI}
	return signCertificate(t, "client", template, caCert, key, caKey)
}

func signCertificate(t *testing.T, name string, template, parent *x509.Certificate, key, parentKey *ecdsa.PrivateKey) certMaterial {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatalf("create %s certificate: %v", name, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse %s certificate: %v", name, err)
	}
	return certMaterial{der: der, cert: cert, key: key}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func certificateTemplate(t *testing.T, commonName string, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
}

func writeCert(t *testing.T, dir, name string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write cert %s: %v", name, err)
	}
	return path
}

func writeKey(t *testing.T, dir, name string, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write key %s: %v", name, err)
	}
	return path
}
