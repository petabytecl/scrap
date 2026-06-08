package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/security"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestAppSecurityRuntimeLoadsProductionAuthorizer(t *testing.T) {
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "scrap.local",
	})
	cfg := Config{
		SecurityMode: security.ModeProduction,
		ProductionGates: security.StartupGateConfig{
			Mode: security.ModeProduction,
			TLS: security.TLSConfig{
				Public:   productionTLSFiles(bundle),
				Peer:     productionTLSFiles(bundle),
				Admin:    productionTLSFiles(bundle),
				Scrapctl: productionTLSFiles(bundle),
			},
			RolePolicyPath: writeRolePolicy(t, t.TempDir()),
		},
	}

	runtime, err := newAppSecurityRuntimeOptions(cfg)
	if err != nil {
		t.Fatalf("newAppSecurityRuntimeOptions: %v", err)
	}
	if runtime.authorizer == nil {
		t.Fatal("production runtime authorizer is nil")
	}
	if len(runtime.publicGRPCOptions) == 0 || len(runtime.peerGRPCOptions) == 0 || !runtime.adminTLS.enabled {
		t.Fatalf("runtime TLS/authz options not configured: %+v", runtime)
	}
}

func TestAppSecurityRuntimeLeavesDevelopmentAuthorizerUnset(t *testing.T) {
	runtime, err := newAppSecurityRuntimeOptions(Config{SecurityMode: security.ModeDevelopment})
	if err != nil {
		t.Fatalf("newAppSecurityRuntimeOptions: %v", err)
	}
	if runtime.authorizer != nil {
		t.Fatal("development runtime authorizer should be nil")
	}
}

func productionTLSFiles(bundle securityfixture.CertBundle) security.TLSFiles {
	return security.TLSFiles{
		ServerCertPath: bundle.ServerCertPath,
		ServerKeyPath:  bundle.ServerKeyPath,
		ClientCAPath:   bundle.CACertPath,
		ServerName:     "scrap.local",
	}
}

func writeRolePolicy(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "roles.json")
	data, err := json.Marshal(map[string]any{
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
				"id":    "spiffe://scrap/cell/cell-a/member/member-a/member-1",
				"roles": []string{"document_writer", "document_reader", "peer_member", "admin_reader", "admin_operator"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal role policy: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write role policy: %v", err)
	}
	return path
}
