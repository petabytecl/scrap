package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseModeAcceptsExplicitModes(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		want       Mode
		production bool
	}{
		{name: "production", raw: "production", want: ModeProduction, production: true},
		{name: "development", raw: "development", want: ModeDevelopment},
		{name: "test", raw: "test", want: ModeTest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMode(tt.raw)
			if err != nil {
				t.Fatalf("ParseMode(%q): %v", tt.raw, err)
			}
			assertMode(t, got, tt.want, tt.raw, tt.production)
		})
	}
}

func assertMode(t *testing.T, got, want Mode, raw string, production bool) {
	t.Helper()

	if got != want {
		t.Fatalf("ParseMode(%q) = %q, want %q", raw, got, want)
	}
	if got.String() != raw {
		t.Fatalf("String() = %q, want %q", got.String(), raw)
	}
	if got.IsProduction() != production {
		t.Fatalf("IsProduction() = %t, want %t", got.IsProduction(), production)
	}
	if got.IsNonProduction() == production {
		t.Fatalf("IsNonProduction() = %t, want %t", got.IsNonProduction(), !production)
	}
}

func TestParseModeRejectsUnsetOrUnknownMode(t *testing.T) {
	for _, raw := range []string{"", " ", "prod", "dev", "staging", "Production"} {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseMode(raw)
			if err == nil {
				t.Fatalf("ParseMode(%q) succeeded, want error", raw)
			}
			if got := ErrorClass(err); got != ClassSecurityMode {
				t.Fatalf("ErrorClass() = %q, want %q", got, ClassSecurityMode)
			}
		})
	}
}

func TestProductionStartupGatesRejectMissingClassesIndependently(t *testing.T) {
	base := validProductionGateConfig(t)
	tests := []struct {
		name   string
		mutate func(*StartupGateConfig)
		want   ConfigClass
	}{
		{name: "tls", mutate: func(c *StartupGateConfig) { c.TLS = TLSConfig{} }, want: ClassTLSConfig},
		{name: "role policy", mutate: func(c *StartupGateConfig) { c.RolePolicyPath = "" }, want: ClassRolePolicy},
		{name: "peer identity", mutate: func(c *StartupGateConfig) { c.PeerIdentityPolicyPath = "" }, want: ClassPeerIdentityPolicy},
		{name: "transit", mutate: func(c *StartupGateConfig) { c.Transit = TransitConfig{} }, want: ClassTransitConfig},
		{name: "audit", mutate: func(c *StartupGateConfig) { c.AuditSink = AuditSinkConfig{} }, want: ClassAuditConfig},
		{name: "rate limits", mutate: func(c *StartupGateConfig) { c.RateLimits = RateLimitConfig{} }, want: ClassRateLimitConfig},
		{name: "test hooks", mutate: func(c *StartupGateConfig) { c.TestHooks = true }, want: ClassDangerousHooks},
		{name: "pprof", mutate: func(c *StartupGateConfig) { c.Pprof.Enabled = true }, want: ClassDangerousHooks},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			err := ValidateStartupGates(cfg)
			if err == nil {
				t.Fatalf("ValidateStartupGates() succeeded, want %s error", tt.want)
			}
			if got := ErrorClass(err); got != tt.want {
				t.Fatalf("ErrorClass() = %q, want %q; err=%v", got, tt.want, err)
			}
		})
	}
}

func TestNonProductionModesDoNotRequireProductionCredentials(t *testing.T) {
	for _, mode := range []Mode{ModeDevelopment, ModeTest} {
		t.Run(mode.String(), func(t *testing.T) {
			if err := ValidateStartupGates(StartupGateConfig{Mode: mode}); err != nil {
				t.Fatalf("ValidateStartupGates(%s): %v", mode, err)
			}

			readiness := ProductionReadinessForMode(mode)
			if readiness.Status != ReadinessStatusNotReady {
				t.Fatalf("readiness status = %q, want %q", readiness.Status, ReadinessStatusNotReady)
			}
			if readiness.Reason != ReadinessReasonNonProductionSecurityMode {
				t.Fatalf("readiness reason = %q, want %q", readiness.Reason, ReadinessReasonNonProductionSecurityMode)
			}
		})
	}
}

func TestProductionModeReportsReadyWhenStartupGatesPass(t *testing.T) {
	cfg := validProductionGateConfig(t)
	if err := ValidateStartupGates(cfg); err != nil {
		t.Fatalf("ValidateStartupGates(valid production config): %v", err)
	}

	readiness := ProductionReadinessForMode(ModeProduction)
	if readiness.Status != ReadinessStatusReady {
		t.Fatalf("readiness status = %q, want %q", readiness.Status, ReadinessStatusReady)
	}
	if readiness.Reason != "" {
		t.Fatalf("readiness reason = %q, want empty", readiness.Reason)
	}
}

func validProductionGateConfig(t *testing.T) StartupGateConfig {
	t.Helper()

	dir := t.TempDir()
	certPath, keyPath, caPath := writeTLSFixture(t, dir, "scrapd.local")
	rolePolicyPath := writeJSONFixture(t, dir, "roles.json", map[string]any{
		"roles": []string{
			"document_writer",
			"document_reader",
			"peer_member",
			"admin_reader",
			"admin_operator",
			"admin_break_glass",
		},
		"principals": []map[string]any{
			{
				"id": "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a",
				"roles": []string{
					"document_writer",
					"document_reader",
					"peer_member",
					"admin_reader",
					"admin_operator",
					"admin_break_glass",
				},
			},
		},
	})
	peerIdentityPolicyPath := writeJSONFixture(t, dir, "peer-identity.json", map[string]any{
		"cell_id":         "cell-a",
		"member_hostname": "scrapd-0",
		"member_id":       "member-a",
	})
	auditPolicyPath := writeJSONFixture(t, dir, "audit.json", map[string]any{"sink": "stderr"})
	rateLimitPolicyPath := writeJSONFixture(t, dir, "rate-limit.json", map[string]any{"default_rps": 100})

	tlsFiles := TLSFiles{
		ServerCertPath: certPath,
		ServerKeyPath:  keyPath,
		ClientCAPath:   caPath,
		ServerName:     "scrapd.local",
	}
	return StartupGateConfig{
		Mode:                   ModeProduction,
		TLS:                    TLSConfig{Public: tlsFiles, Peer: tlsFiles, Admin: tlsFiles, Scrapctl: tlsFiles},
		RolePolicyPath:         rolePolicyPath,
		PeerIdentityPolicyPath: peerIdentityPolicyPath,
		PeerIdentity: PeerIdentityConfig{
			CellID:         "cell-a",
			MemberHostname: "scrapd-0",
			MemberID:       "member-a",
		},
		Transit: TransitConfig{
			Address:      "https://openbao.example.invalid",
			MountPath:    "transit",
			KeyName:      "scrap-documents",
			TokenEnv:     "OPENBAO_TOKEN",
			TokenPresent: true,
		},
		AuditSink:  AuditSinkConfig{PolicyPath: auditPolicyPath},
		RateLimits: RateLimitConfig{PolicyPath: rateLimitPolicyPath},
	}
}

func writeJSONFixture(t *testing.T, dir, name string, value any) string {
	t.Helper()

	path := filepath.Join(dir, name)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func writeTLSFixture(t *testing.T, dir, dnsName string) (certPath, keyPath, caPath string) {
	t.Helper()

	return writeTLSFixtureWithValidity(t, dir, dnsName, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

func writeTLSFixtureWithValidity(t *testing.T, dir, dnsName string, notBefore, notAfter time.Time) (certPath, keyPath, caPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPath = filepath.Join(dir, "server.crt")
	keyPath = filepath.Join(dir, "server.key")
	caPath = filepath.Join(dir, "client-ca.crt")
	writePEMFixture(t, certPath, "CERTIFICATE", certDER)
	writePEMFixture(t, keyPath, "EC PRIVATE KEY", keyDER)
	writePEMFixture(t, caPath, "CERTIFICATE", certDER)
	return certPath, keyPath, caPath
}

func writePEMFixture(t *testing.T, path, typ string, der []byte) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // Test fixture path is generated under t.TempDir.
	if err != nil {
		t.Fatalf("open %s: %v", filepath.Base(path), err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close %s: %v", filepath.Base(path), err)
		}
	}()
	if err := pem.Encode(file, &pem.Block{Type: typ, Bytes: der}); err != nil {
		t.Fatalf("encode %s: %v", filepath.Base(path), err)
	}
}
