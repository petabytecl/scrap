package security

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// ClientTLSFiles points at one client's presented certificate, key, trusted
// server CA bundle, and expected server identity.
type ClientTLSFiles struct {
	CertPath   string
	KeyPath    string
	RootCAPath string
	ServerName string
}

// ClientTLSFilesFromSurface adapts the existing per-surface TLS environment
// shape for clients. For client use, ServerCertPath/ServerKeyPath hold the
// presented client certificate/key, and ClientCAPath is the trusted server CA.
func ClientTLSFilesFromSurface(files TLSFiles) ClientTLSFiles {
	return ClientTLSFiles{
		CertPath:   files.ServerCertPath,
		KeyPath:    files.ServerKeyPath,
		RootCAPath: files.ClientCAPath,
		ServerName: files.ServerName,
	}
}

func BuildMTLSServerConfig(key string, files TLSFiles) (*tls.Config, error) {
	key = tlsKey(key)
	if err := requireTLSFiles(key, files); err != nil {
		return nil, err
	}
	cert, err := loadTLSCertificate(key, files)
	if err != nil {
		return nil, err
	}
	if err := validateServerCertificate(key, cert, files.ServerName); err != nil {
		return nil, err
	}
	clientCAs, err := loadCertPool(key, files.ClientCAPath, "client CA bundle")
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}, nil
}

func BuildMTLSClientConfig(key string, files ClientTLSFiles) (*tls.Config, error) {
	key = tlsKey(key)
	if err := requireClientTLSFiles(key, files); err != nil {
		return nil, err
	}
	cert, err := loadClientTLSCertificate(key, files)
	if err != nil {
		return nil, err
	}
	if err := validateClientCertificate(key, cert); err != nil {
		return nil, err
	}
	rootCAs, err := loadCertPool(key, files.RootCAPath, "server CA bundle")
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   strings.TrimSpace(files.ServerName),
	}, nil
}

func tlsKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "SCRAP_TLS"
	}
	return key
}

func requireClientTLSFiles(key string, files ClientTLSFiles) error {
	if strings.TrimSpace(files.CertPath) == "" ||
		strings.TrimSpace(files.KeyPath) == "" ||
		strings.TrimSpace(files.RootCAPath) == "" ||
		strings.TrimSpace(files.ServerName) == "" {
		return newGateError(ClassTLSConfig, key, "client cert, client key, server CA, and server name are required")
	}
	return nil
}

func loadClientTLSCertificate(key string, files ClientTLSFiles) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(files.CertPath, files.KeyPath)
	if err != nil {
		return tls.Certificate{}, newGateError(ClassTLSConfig, key, "client certificate/key pair is invalid")
	}
	return cert, nil
}

func validateClientCertificate(key string, cert tls.Certificate) error {
	leaf, err := leafCertificate(cert)
	if err != nil {
		return newGateError(ClassTLSConfig, key, "client certificate is invalid")
	}
	if err := validateCertificateValidity(key, leaf, "client certificate"); err != nil {
		return err
	}
	if !hasExtKeyUsage(leaf, x509.ExtKeyUsageClientAuth) {
		return newGateError(ClassTLSConfig, key, "client certificate is invalid")
	}
	return nil
}

func loadCertPool(key, path, description string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(path) //nolint:gosec // Operator-configured TLS CA path is the intended startup gate input.
	if err != nil {
		return nil, newGateError(ClassTLSConfig, key, description+" is invalid")
	}
	certs, ok := parseCertificateBundle(caPEM)
	if !ok {
		return nil, newGateError(ClassTLSConfig, key, description+" is invalid")
	}
	pool := x509.NewCertPool()
	for _, cert := range certs {
		if err := validateCertificateValidity(key, cert, description+" certificate"); err != nil {
			return nil, err
		}
		if !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, newGateError(ClassTLSConfig, key, description+" is invalid")
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func leafCertificate(cert tls.Certificate) (*x509.Certificate, error) {
	if cert.Leaf != nil {
		return cert.Leaf, nil
	}
	if len(cert.Certificate) == 0 {
		return nil, errMissingCertificate
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	return leaf, nil
}

func hasExtKeyUsage(cert *x509.Certificate, usage x509.ExtKeyUsage) bool {
	if len(cert.ExtKeyUsage) == 0 {
		return true
	}
	for _, candidate := range cert.ExtKeyUsage {
		if candidate == usage {
			return true
		}
	}
	return false
}
