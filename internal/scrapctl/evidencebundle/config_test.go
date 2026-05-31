package evidencebundle

import (
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsEndpointsAndRuntime(t *testing.T) {
	cfg, err := Config{
		RepoRoot:  t.TempDir(),
		BundleDir: t.TempDir(),
	}.withDefaults()
	if err != nil {
		t.Fatalf("withDefaults: %v", err)
	}

	if cfg.GrafanaURL != defaultGrafanaURL {
		t.Fatalf("grafana URL = %q", cfg.GrafanaURL)
	}
	if !strings.HasSuffix(cfg.MimirProxy, "/api/datasources/proxy/uid/mimir") {
		t.Fatalf("mimir proxy = %q", cfg.MimirProxy)
	}
	if cfg.Scenario != "throughput" {
		t.Fatalf("scenario = %q", cfg.Scenario)
	}
	if cfg.Workers != defaultWorkers {
		t.Fatalf("workers = %d", cfg.Workers)
	}
	if cfg.ProbeTimeout != defaultProbeTimeout {
		t.Fatalf("probe timeout = %s", cfg.ProbeTimeout)
	}
}

func TestConfigRejectsInvalidScenario(t *testing.T) {
	_, err := Config{RepoRoot: t.TempDir(), BundleDir: t.TempDir(), Scenario: "unknown"}.withDefaults()
	if err == nil || !strings.Contains(err.Error(), "unsupported scenario") {
		t.Fatalf("error = %v, want unsupported scenario", err)
	}
}

func TestValidateConfiguredDurations(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "negative settle",
			cfg:  Config{Settle: -time.Second, ProbeTimeout: time.Second, Duration: "1s"},
			want: "settle must be non-negative",
		},
		{
			name: "zero probe",
			cfg:  Config{ProbeTimeout: 0, Duration: "1s"},
			want: "probe timeout must be positive",
		},
		{
			name: "bad duration",
			cfg:  Config{ProbeTimeout: time.Second, Duration: "nope"},
			want: "parse stress duration",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfiguredDurations(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
