package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionStartupGatesRejectInvalidTLSInputsWithoutPathLeak(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, cfg *StartupGateConfig)
	}{
		{
			name: "missing cert file",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.TLS.Public.ServerCertPath = filepath.Join(t.TempDir(), "missing.crt")
			},
		},
		{
			name: "invalid cert pem",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				replaceFile(t, cfg.TLS.Public.ServerCertPath, []byte("not pem"))
			},
		},
		{
			name: "mismatched key pair",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatalf("generate mismatched key: %v", err)
				}
				keyDER, err := x509.MarshalECPrivateKey(key)
				if err != nil {
					t.Fatalf("marshal mismatched key: %v", err)
				}
				replacePEMFile(t, cfg.TLS.Public.ServerKeyPath, "EC PRIVATE KEY", keyDER)
			},
		},
		{
			name: "invalid client CA bundle",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				replaceFile(t, cfg.TLS.Public.ClientCAPath, []byte("not pem"))
			},
		},
		{
			name: "client CA certificate not CA",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				replaceClientCAFixture(t, cfg.TLS.Public.ClientCAPath, false, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
			},
		},
		{
			name: "expired client CA certificate",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				replaceClientCAFixture(t, cfg.TLS.Public.ClientCAPath, true, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
			},
		},
		{
			name: "expired certificate",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				certPath, keyPath, caPath := writeTLSFixtureWithValidity(t, t.TempDir(), "scrapd.local", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
				cfg.TLS.Public = TLSFiles{
					ServerCertPath: certPath,
					ServerKeyPath:  keyPath,
					ClientCAPath:   caPath,
					ServerName:     "scrapd.local",
				}
			},
		},
		{
			name: "identity mismatch",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.TLS.Public.ServerName = "other.local"
			},
		},
		{
			name: "missing server name",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.TLS.Public.ServerName = ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validProductionGateConfig(t)
			tt.mutate(t, &cfg)

			err := ValidateStartupGates(cfg)
			if err == nil {
				t.Fatal("ValidateStartupGates() succeeded, want TLS error")
			}
			if got := ErrorClass(err); got != ClassTLSConfig {
				t.Fatalf("ErrorClass() = %q, want %q; err=%v", got, ClassTLSConfig, err)
			}
			assertErrorOmitsTLSPaths(t, err, cfg.TLS.Public)
		})
	}
}

func TestProductionStartupGatesRejectInvalidPolicyAndTransitInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, cfg *StartupGateConfig)
		want   ConfigClass
	}{
		{
			name: "unknown role",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.RolePolicyPath = writeJSONFixture(t, t.TempDir(), "roles.json", map[string]any{"roles": []string{"document_writer", "root"}})
			},
			want: ClassRolePolicy,
		},
		{
			name: "incomplete peer identity",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.PeerIdentityPolicyPath = writeJSONFixture(t, t.TempDir(), "peer.json", map[string]any{"cell_id": "cell-a"})
			},
			want: ClassPeerIdentityPolicy,
		},
		{
			name: "contradictory peer identity",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.PeerIdentity.MemberID = "member-b"
			},
			want: ClassPeerIdentityPolicy,
		},
		{
			name: "fake transit",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.Fake = true
			},
			want: ClassTransitConfig,
		},
		{
			name: "missing transit token secret",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.TokenPresent = false
			},
			want: ClassTransitConfig,
		},
		{
			name: "invalid transit address",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.Address = "openbao.internal"
			},
			want: ClassTransitConfig,
		},
		{
			name: "http transit address",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.Address = "http://openbao.internal"
			},
			want: ClassTransitConfig,
		},
		{
			name: "invalid transit mount path",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.MountPath = "../transit"
			},
			want: ClassTransitConfig,
		},
		{
			name: "invalid transit key path",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.Transit.KeyName = "documents//active"
			},
			want: ClassTransitConfig,
		},
		{
			name: "invalid audit policy json",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.AuditSink.PolicyPath = writeRawFixture(t, t.TempDir(), "audit.json", []byte("{"))
			},
			want: ClassAuditConfig,
		},
		{
			name: "empty audit policy json",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.AuditSink.PolicyPath = writeJSONFixture(t, t.TempDir(), "audit.json", map[string]any{})
			},
			want: ClassAuditConfig,
		},
		{
			name: "invalid rate limit policy json",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.RateLimits.PolicyPath = writeRawFixture(t, t.TempDir(), "rate.json", []byte("{"))
			},
			want: ClassRateLimitConfig,
		},
		{
			name: "null rate limit policy json",
			mutate: func(t *testing.T, cfg *StartupGateConfig) {
				t.Helper()
				cfg.RateLimits.PolicyPath = writeRawFixture(t, t.TempDir(), "rate.json", []byte("null"))
			},
			want: ClassRateLimitConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validProductionGateConfig(t)
			tt.mutate(t, &cfg)

			err := ValidateStartupGates(cfg)
			if err == nil {
				t.Fatalf("ValidateStartupGates() succeeded, want %s error", tt.want)
			}
			if got := ErrorClass(err); got != tt.want {
				t.Fatalf("ErrorClass() = %q, want %q; err=%v", got, tt.want, err)
			}
			if strings.Contains(err.Error(), "openbao.internal") || strings.Contains(err.Error(), "http://openbao.internal") || strings.Contains(err.Error(), "../transit") {
				t.Fatalf("startup gate error leaked Transit config value: %v", err)
			}
		})
	}
}

func assertErrorOmitsTLSPaths(t *testing.T, err error, files TLSFiles) {
	t.Helper()

	msg := err.Error()
	for _, path := range []string{files.ServerCertPath, files.ServerKeyPath, files.ClientCAPath} {
		if path != "" && strings.Contains(msg, path) {
			t.Fatalf("error leaked TLS path %q: %v", path, err)
		}
	}
}

func replacePEMFile(t *testing.T, path, typ string, der []byte) {
	t.Helper()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", filepath.Base(path), err)
	}
	writePEMFixture(t, path, typ, der)
}

func replaceClientCAFixture(t *testing.T, path string, isCA bool, notBefore, notAfter time.Time) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client CA key: %v", err)
	}
	usage := x509.KeyUsageDigitalSignature
	if isCA {
		usage |= x509.KeyUsageCertSign
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "client-ca"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              usage,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create client CA certificate: %v", err)
	}
	replacePEMFile(t, path, "CERTIFICATE", certDER)
}

func replaceFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

func writeRawFixture(t *testing.T, dir, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	replaceFile(t, path, data)
	return path
}
