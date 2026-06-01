package shard

import (
	"strings"
	"testing"
	"time"
)

func TestParseEvictionConfigFromEnvDefaults(t *testing.T) {
	clearEvictionConfigEnv(t)

	cfg, err := ParseEvictionConfigFromEnv()
	if err != nil {
		t.Fatalf("ParseEvictionConfigFromEnv: %v", err)
	}

	if cfg.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if cfg.HotResidencyWindow != DefaultEvictionHotResidencyWindow {
		t.Fatalf("HotResidencyWindow = %v, want %v", cfg.HotResidencyWindow, DefaultEvictionHotResidencyWindow)
	}
	if cfg.PlanTTL != DefaultEvictionPlanTTL {
		t.Fatalf("PlanTTL = %v, want %v", cfg.PlanTTL, DefaultEvictionPlanTTL)
	}
	if cfg.RecommendedMaxBlocks != DefaultEvictionRecommendedMaxBlocks {
		t.Fatalf("RecommendedMaxBlocks = %d, want %d", cfg.RecommendedMaxBlocks, DefaultEvictionRecommendedMaxBlocks)
	}
	if cfg.RecommendedMaxBytes != DefaultEvictionRecommendedMaxBytes {
		t.Fatalf("RecommendedMaxBytes = %d, want %d", cfg.RecommendedMaxBytes, DefaultEvictionRecommendedMaxBytes)
	}
	if cfg.MaxValidateSamples != DefaultEvictionMaxValidateSamples {
		t.Fatalf("MaxValidateSamples = %d, want %d", cfg.MaxValidateSamples, DefaultEvictionMaxValidateSamples)
	}
}

func TestParseEvictionConfigFromEnvValidOverrides(t *testing.T) {
	clearEvictionConfigEnv(t)
	t.Setenv("SCRAP_EVICTION_ENABLED", "true")
	t.Setenv("SCRAP_EVICTION_HOT_RESIDENCY_WINDOW", "30m")
	t.Setenv("SCRAP_EVICTION_PLAN_TTL", "45s")
	t.Setenv("SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS", "3")
	t.Setenv("SCRAP_EVICTION_RECOMMENDED_MAX_BYTES", "4096")
	t.Setenv("SCRAP_EVICTION_MAX_VALIDATE_SAMPLES", "0")

	cfg, err := ParseEvictionConfigFromEnv()
	if err != nil {
		t.Fatalf("ParseEvictionConfigFromEnv: %v", err)
	}

	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
	if cfg.HotResidencyWindow != 30*time.Minute {
		t.Fatalf("HotResidencyWindow = %v, want 30m", cfg.HotResidencyWindow)
	}
	if cfg.PlanTTL != 45*time.Second {
		t.Fatalf("PlanTTL = %v, want 45s", cfg.PlanTTL)
	}
	if cfg.RecommendedMaxBlocks != 3 {
		t.Fatalf("RecommendedMaxBlocks = %d, want 3", cfg.RecommendedMaxBlocks)
	}
	if cfg.RecommendedMaxBytes != 4096 {
		t.Fatalf("RecommendedMaxBytes = %d, want 4096", cfg.RecommendedMaxBytes)
	}
	if cfg.MaxValidateSamples != 0 {
		t.Fatalf("MaxValidateSamples = %d, want 0", cfg.MaxValidateSamples)
	}
}

func TestParseEvictionConfigFromEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{"enabled bool", "SCRAP_EVICTION_ENABLED", "sometimes", "SCRAP_EVICTION_ENABLED"},
		{"hot residency duration", "SCRAP_EVICTION_HOT_RESIDENCY_WINDOW", "tomorrow", "SCRAP_EVICTION_HOT_RESIDENCY_WINDOW"},
		{"hot residency negative", "SCRAP_EVICTION_HOT_RESIDENCY_WINDOW", "-1s", "SCRAP_EVICTION_HOT_RESIDENCY_WINDOW"},
		{"plan ttl zero", "SCRAP_EVICTION_PLAN_TTL", "0s", "SCRAP_EVICTION_PLAN_TTL"},
		{"recommended blocks zero", "SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS", "0", "SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS"},
		{"recommended bytes negative", "SCRAP_EVICTION_RECOMMENDED_MAX_BYTES", "-1", "SCRAP_EVICTION_RECOMMENDED_MAX_BYTES"},
		{"validate samples negative", "SCRAP_EVICTION_MAX_VALIDATE_SAMPLES", "-1", "SCRAP_EVICTION_MAX_VALIDATE_SAMPLES"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEvictionConfigEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := ParseEvictionConfigFromEnv()
			if err == nil {
				t.Fatal("ParseEvictionConfigFromEnv succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not name %s", err, tt.wantErr)
			}
		})
	}
}

func clearEvictionConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SCRAP_EVICTION_ENABLED",
		"SCRAP_EVICTION_HOT_RESIDENCY_WINDOW",
		"SCRAP_EVICTION_PLAN_TTL",
		"SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS",
		"SCRAP_EVICTION_RECOMMENDED_MAX_BYTES",
		"SCRAP_EVICTION_MAX_VALIDATE_SAMPLES",
	} {
		t.Setenv(key, "")
	}
}
