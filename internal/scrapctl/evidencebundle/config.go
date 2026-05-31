package evidencebundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (cfg Config) withDefaults() (Config, error) {
	out := cfg
	repoRoot, err := defaultRepoRoot(out.RepoRoot)
	if err != nil {
		return Config{}, err
	}
	out.RepoRoot = repoRoot
	bundleDir, err := defaultBundleDir(out.BundleDir, out.RepoRoot)
	if err != nil {
		return Config{}, err
	}
	out.BundleDir = bundleDir

	out.applyEndpointDefaults()
	out.applyRuntimeDefaults()
	return out, validateScenario(out.Scenario)
}

func defaultRepoRoot(value string) (string, error) {
	if value == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		value = cwd
	}
	out, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	return out, nil
}

func defaultBundleDir(value, repoRoot string) (string, error) {
	if value == "" {
		value = filepath.Join(repoRoot, defaultBundleDirName)
	}
	out, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve bundle dir: %w", err)
	}
	return out, nil
}

func (cfg *Config) applyEndpointDefaults() {
	if cfg.GrafanaURL == "" {
		cfg.GrafanaURL = defaultGrafanaURL
	}
	grafanaURL := strings.TrimRight(cfg.GrafanaURL, "/")
	if cfg.AdminURL == "" {
		cfg.AdminURL = defaultAdminURL
	}
	if cfg.MimirProxy == "" {
		cfg.MimirProxy = grafanaURL + "/api/datasources/proxy/uid/mimir"
	}
	if cfg.TempoProxy == "" {
		cfg.TempoProxy = grafanaURL + "/api/datasources/proxy/uid/tempo"
	}
	if cfg.LokiProxy == "" {
		cfg.LokiProxy = grafanaURL + "/api/datasources/proxy/uid/loki"
	}
	if cfg.PyroscopeURL == "" {
		cfg.PyroscopeURL = grafanaURL + "/api/datasources/proxy/uid/pyroscope"
	}
}

func (cfg *Config) applyRuntimeDefaults() {
	if cfg.Kubectl == "" {
		cfg.Kubectl = defaultKubectl
	}
	if cfg.Namespace == "" {
		cfg.Namespace = defaultNamespace
	}
	if cfg.Scenario == "" {
		cfg.Scenario = "throughput"
	}
	if cfg.StressAddr == "" {
		cfg.StressAddr = defaultStressAddr
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.Duration == "" {
		cfg.Duration = defaultDuration
	}
	if cfg.DocSizeBytes <= 0 {
		cfg.DocSizeBytes = defaultDocSizeBytes
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = defaultProbeTimeout
	}
	if cfg.GoCommand == "" {
		cfg.GoCommand = defaultGoCommand
	}
}

func validateScenario(scenario string) error {
	switch scenario {
	case "throughput", "mixed", "pressure":
		return nil
	default:
		return fmt.Errorf("unsupported scenario %q", scenario)
	}
}

func requireNonNegativeDuration(name string, value time.Duration) error {
	if value < 0 {
		return fmt.Errorf("%s must be non-negative", name)
	}
	return nil
}

func requirePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	return nil
}

func validateConfiguredDurations(cfg Config) error {
	if err := requireNonNegativeDuration("settle", cfg.Settle); err != nil {
		return err
	}
	if err := requirePositiveDuration("probe timeout", cfg.ProbeTimeout); err != nil {
		return err
	}
	if _, err := time.ParseDuration(cfg.Duration); err != nil {
		return fmt.Errorf("parse stress duration %q: %w", cfg.Duration, err)
	}
	return nil
}
