package cmd

import (
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/shard"
)

// configEnvKeys are every environment variable loadConfig reads at the
// cmd/scrapd level. Tests clear them so the process environment cannot leak in.
var configEnvKeys = []string{
	"SCRAP_CELL_ID",
	"SCRAP_HEADLESS_SERVICE",
	"POD_NAMESPACE",
	"SCRAP_UPLOAD_ENABLED",
	"SCRAP_TELEMETRY_RAW_IDS",
	"SCRAP_TEST_HOOKS",
	"SCRAP_PPROF_ENABLED",
	"SCRAP_UPLOAD_CONCURRENCY",
	"SCRAP_REPLICAS",
	"SCRAP_PEER_PORT",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	clearConfigEnv(t)

	c, err := loadConfig(nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", c.DataDir)
	}
	if c.BlockSealSize != shard.DefaultBlockSealSize {
		t.Errorf("BlockSealSize = %d, want %d", c.BlockSealSize, shard.DefaultBlockSealSize)
	}
	if !c.UploadEnabled {
		t.Error("UploadEnabled = false, want true (default)")
	}
	if c.UploadConcurrency != shard.DefaultUploadConcurrency {
		t.Errorf("UploadConcurrency = %d, want %d", c.UploadConcurrency, shard.DefaultUploadConcurrency)
	}
	if c.PeerPort != defaultPeerPort {
		t.Errorf("PeerPort = %d, want %d", c.PeerPort, defaultPeerPort)
	}
	if c.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", c.Namespace)
	}
}

func TestLoadConfigValid(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SCRAP_UPLOAD_ENABLED", "false")
	t.Setenv("SCRAP_UPLOAD_CONCURRENCY", "8")
	t.Setenv("SCRAP_REPLICAS", "3")
	t.Setenv("SCRAP_PEER_PORT", "7000")
	t.Setenv("SCRAP_TEST_HOOKS", "true")
	t.Setenv("SCRAP_PPROF_ENABLED", "1")
	t.Setenv("SCRAP_CELL_ID", "cell-a")

	c, err := loadConfig([]string{"-data-dir=/srv", "-block-seal-size=1024", "-peers=1=h:1"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DataDir", c.DataDir, "/srv"},
		{"BlockSealSize", c.BlockSealSize, int64(1024)},
		{"PeersFlag", c.PeersFlag, "1=h:1"},
		{"UploadEnabled", c.UploadEnabled, false},
		{"UploadConcurrency", c.UploadConcurrency, 8},
		{"Replicas", c.Replicas, 3},
		{"PeerPort", c.PeerPort, 7000},
		{"TestHooks", c.TestHooks, true},
		{"PprofEnabled", c.PprofEnabled, true},
		{"CellID", c.CellID, "cell-a"},
	}
	for _, ck := range checks {
		if ck.got != ck.want {
			t.Errorf("%s = %v, want %v", ck.name, ck.got, ck.want)
		}
	}
}

func TestLoadConfigRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string // substring the error must contain (the offending key)
	}{
		{"malformed int", nil, map[string]string{"SCRAP_UPLOAD_CONCURRENCY": "two"}, "SCRAP_UPLOAD_CONCURRENCY"},
		{"malformed bool", nil, map[string]string{"SCRAP_UPLOAD_ENABLED": "yesplease"}, "SCRAP_UPLOAD_ENABLED"},
		{"out-of-range port", nil, map[string]string{"SCRAP_PEER_PORT": "99999"}, "SCRAP_PEER_PORT"},
		{"non-positive seal size", []string{"-block-seal-size=0"}, nil, "block-seal-size"},
		{"negative replicas", nil, map[string]string{"SCRAP_REPLICAS": "-1"}, "SCRAP_REPLICAS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := loadConfig(tt.args)
			if err == nil {
				t.Fatalf("expected error naming %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not name offending key %q", err, tt.wantErr)
			}
		})
	}
}
