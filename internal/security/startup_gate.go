package security

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"time"
)

// StartupGateConfig is the production startup security gate input.
type StartupGateConfig struct {
	Mode                   Mode
	TLS                    TLSConfig
	RolePolicyPath         string
	PeerIdentityPolicyPath string
	PeerIdentity           PeerIdentityConfig
	Transit                TransitConfig
	AuditSink              AuditSinkConfig
	RateLimits             RateLimitConfig
	TestHooks              bool
	Pprof                  PprofConfig
}

// TLSConfig contains the required production TLS material by surface.
type TLSConfig struct {
	Public   TLSFiles
	Peer     TLSFiles
	Admin    TLSFiles
	Scrapctl TLSFiles
}

// TLSFiles points at one surface's server certificate, key, and client CA.
type TLSFiles struct {
	ServerCertPath string
	ServerKeyPath  string
	ClientCAPath   string
	ServerName     string
}

// TransitConfig describes the required production Transit configuration.
type TransitConfig struct {
	Address      string
	MountPath    string
	KeyName      string
	TokenEnv     string
	TokenPresent bool
	Fake         bool
}

// PeerIdentityConfig is the configured runtime Cell/Member identity expected in
// the production peer identity policy.
type PeerIdentityConfig struct {
	CellID         string
	MemberHostname string
	MemberID       string
}

// AuditSinkConfig points at the production audit sink policy.
type AuditSinkConfig struct {
	PolicyPath string
}

// RateLimitConfig points at the production rate-limit policy.
type RateLimitConfig struct {
	PolicyPath string
}

// PprofConfig describes dangerous diagnostic hook state.
type PprofConfig struct {
	Enabled bool
}

// ValidateStartupGates validates production-only security gates.
func ValidateStartupGates(cfg StartupGateConfig) error {
	required, err := productionGatesRequired(cfg.Mode)
	if err != nil || !required {
		return err
	}
	return validateProductionStartupGates(cfg)
}

func productionGatesRequired(mode Mode) (bool, error) {
	switch {
	case mode.IsNonProduction():
		return false, nil
	case mode.IsProduction():
		return true, nil
	default:
		return false, newGateError(ClassSecurityMode, "SCRAP_SECURITY_MODE", "must be production, development, or test")
	}
}

func validateProductionStartupGates(cfg StartupGateConfig) error {
	validators := []func() error{
		func() error { return validateTLSConfig(cfg.TLS) },
		func() error { return validateRolePolicy(cfg.RolePolicyPath) },
		func() error { return validatePeerIdentityPolicy(cfg.PeerIdentityPolicyPath, cfg.PeerIdentity) },
		func() error { return validateTransitConfig(cfg.Transit) },
		func() error {
			return validateJSONPolicy(ClassAuditConfig, "SCRAP_AUDIT_POLICY_FILE", cfg.AuditSink.PolicyPath)
		},
		func() error {
			return validateJSONPolicy(ClassRateLimitConfig, "SCRAP_RATE_LIMIT_POLICY_FILE", cfg.RateLimits.PolicyPath)
		},
		func() error { return validateDangerousHooks(cfg.TestHooks, cfg.Pprof) },
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateDangerousHooks(testHooks bool, pprof PprofConfig) error {
	if testHooks {
		return newGateError(ClassDangerousHooks, "SCRAP_TEST_HOOKS", "test hooks cannot be enabled in production")
	}
	if pprof.Enabled {
		return newGateError(ClassDangerousHooks, "SCRAP_PPROF_ENABLED", "pprof requires break-glass enforcement and audit before production use")
	}
	return nil
}

func validateTLSConfig(cfg TLSConfig) error {
	surfaces := []struct {
		name string
		cfg  TLSFiles
	}{
		{name: "PUBLIC", cfg: cfg.Public},
		{name: "PEER", cfg: cfg.Peer},
		{name: "ADMIN", cfg: cfg.Admin},
		{name: "SCRAPCTL", cfg: cfg.Scrapctl},
	}
	for _, surface := range surfaces {
		if err := validateTLSFiles(surface.name, surface.cfg); err != nil {
			return err
		}
	}
	return nil
}

func validateTLSFiles(surface string, files TLSFiles) error {
	key := "SCRAP_TLS_" + surface
	if err := requireTLSFiles(key, files); err != nil {
		return err
	}
	cert, err := loadTLSCertificate(key, files)
	if err != nil {
		return err
	}
	if err := validateClientCA(key, files.ClientCAPath); err != nil {
		return err
	}
	return validateServerCertificate(key, cert, files.ServerName)
}

func requireTLSFiles(key string, files TLSFiles) error {
	if strings.TrimSpace(files.ServerCertPath) == "" ||
		strings.TrimSpace(files.ServerKeyPath) == "" ||
		strings.TrimSpace(files.ClientCAPath) == "" ||
		strings.TrimSpace(files.ServerName) == "" {
		return newGateError(ClassTLSConfig, key, "server cert, server key, client CA, and server name are required")
	}
	return nil
}

func loadTLSCertificate(key string, files TLSFiles) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(files.ServerCertPath, files.ServerKeyPath)
	if err != nil {
		return tls.Certificate{}, newGateError(ClassTLSConfig, key, "server certificate/key pair is invalid")
	}
	return cert, nil
}

func validateClientCA(key, path string) error {
	caPEM, err := os.ReadFile(path) //nolint:gosec // Operator-configured TLS client CA path is the intended startup gate input.
	if err != nil {
		return newGateError(ClassTLSConfig, key, "client CA bundle is invalid")
	}
	certs, ok := parseCertificateBundle(caPEM)
	if !ok {
		return newGateError(ClassTLSConfig, key, "client CA bundle is invalid")
	}
	for _, cert := range certs {
		if err := validateCertificateValidity(key, cert, "client CA certificate"); err != nil {
			return err
		}
		if !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return newGateError(ClassTLSConfig, key, "client CA bundle is invalid")
		}
	}
	return nil
}

func parseCertificateBundle(data []byte) ([]*x509.Certificate, bool) {
	var certs []*x509.Certificate
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return certs, len(certs) > 0 && len(bytes.TrimSpace(rest)) == 0
		}
		if block.Type != "CERTIFICATE" {
			return nil, false
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, false
		}
		certs = append(certs, cert)
		rest = remaining
	}
}

func validateServerCertificate(key string, cert tls.Certificate, serverName string) error {
	if len(cert.Certificate) == 0 {
		return newGateError(ClassTLSConfig, key, "server certificate is invalid")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return newGateError(ClassTLSConfig, key, "server certificate is invalid")
	}
	if err := validateCertificateValidity(key, leaf, "server certificate"); err != nil {
		return err
	}
	if !hasExtKeyUsage(leaf, x509.ExtKeyUsageServerAuth) {
		return newGateError(ClassTLSConfig, key, "server certificate is invalid")
	}
	return validateCertificateIdentity(key, leaf, serverName)
}

func validateCertificateValidity(key string, cert *x509.Certificate, description string) error {
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return newGateError(ClassTLSConfig, key, description+" is outside its validity window")
	}
	return nil
}

func validateCertificateIdentity(key string, cert *x509.Certificate, serverName string) error {
	if err := cert.VerifyHostname(strings.TrimSpace(serverName)); err != nil {
		return newGateError(ClassTLSConfig, key, "server certificate identity does not match configured name")
	}
	return nil
}

func validateRolePolicy(path string) error {
	_, err := LoadRolePolicy(path)
	return err
}

func validatePeerIdentityPolicy(path string, expected PeerIdentityConfig) error {
	var policy struct {
		CellID         string `json:"cell_id"`
		MemberHostname string `json:"member_hostname"`
		MemberID       string `json:"member_id"`
	}
	if err := readJSONPolicy(ClassPeerIdentityPolicy, "SCRAP_PEER_IDENTITY_POLICY_FILE", path, &policy); err != nil {
		return err
	}
	policyIdentity := PeerIdentityConfig{
		CellID:         strings.TrimSpace(policy.CellID),
		MemberHostname: strings.TrimSpace(policy.MemberHostname),
		MemberID:       strings.TrimSpace(policy.MemberID),
	}
	expected = PeerIdentityConfig{
		CellID:         strings.TrimSpace(expected.CellID),
		MemberHostname: strings.TrimSpace(expected.MemberHostname),
		MemberID:       strings.TrimSpace(expected.MemberID),
	}
	if policyIdentity.CellID == "" || policyIdentity.MemberHostname == "" || policyIdentity.MemberID == "" {
		return newGateError(ClassPeerIdentityPolicy, "SCRAP_PEER_IDENTITY_POLICY_FILE", "cell_id, member_hostname, and member_id are required")
	}
	if expected.CellID == "" || expected.MemberHostname == "" || expected.MemberID == "" {
		return newGateError(ClassPeerIdentityPolicy, "SCRAP_PEER_IDENTITY_POLICY_FILE", "configured cell_id, member_hostname, and member_id are required")
	}
	if policyIdentity != expected {
		return newGateError(ClassPeerIdentityPolicy, "SCRAP_PEER_IDENTITY_POLICY_FILE", "peer identity policy does not match configured Cell/Member identity")
	}
	return nil
}

func validateTransitConfig(cfg TransitConfig) error {
	if cfg.Fake {
		return newGateError(ClassTransitConfig, "SCRAP_TRANSIT_FAKE", "fake Transit cannot be selected in production")
	}
	if strings.TrimSpace(cfg.Address) == "" ||
		strings.TrimSpace(cfg.MountPath) == "" ||
		strings.TrimSpace(cfg.KeyName) == "" ||
		strings.TrimSpace(cfg.TokenEnv) == "" {
		return newGateError(ClassTransitConfig, "SCRAP_TRANSIT_CONFIG", "address, mount path, key name, and token env are required")
	}
	if !cfg.TokenPresent {
		return newGateError(ClassTransitConfig, "SCRAP_TRANSIT_TOKEN_ENV", "referenced Transit token environment variable is required")
	}
	return nil
}

func validateJSONPolicy(class ConfigClass, key, path string) error {
	var raw map[string]any
	if err := readJSONPolicy(class, key, path, &raw); err != nil {
		return err
	}
	if len(raw) == 0 {
		return newGateError(class, key, "policy file must contain at least one setting")
	}
	return nil
}

func readJSONPolicy(class ConfigClass, key, path string, out any) error {
	if path == "" {
		return newGateError(class, key, "policy path is required")
	}
	data, err := os.ReadFile(path) //nolint:gosec // Operator-configured JSON policy path is the intended startup gate input.
	if err != nil {
		return newGateError(class, key, "policy file is unreadable")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return newGateError(class, key, "policy file must be valid JSON")
	}
	return nil
}
