package shard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEvictionHotResidencyWindow   = 24 * time.Hour
	DefaultEvictionPlanTTL              = 10 * time.Minute
	DefaultEvictionRecommendedMaxBlocks = 10
	DefaultEvictionRecommendedMaxBytes  = 640 * 1024 * 1024
	DefaultEvictionMaxValidateSamples   = 1
)

type EvictionConfig struct {
	Enabled              bool
	HotResidencyWindow   time.Duration
	PlanTTL              time.Duration
	RecommendedMaxBlocks int
	RecommendedMaxBytes  int64
	MaxValidateSamples   int
}

func DefaultEvictionConfig() EvictionConfig {
	return EvictionConfig{
		Enabled:              false,
		HotResidencyWindow:   DefaultEvictionHotResidencyWindow,
		PlanTTL:              DefaultEvictionPlanTTL,
		RecommendedMaxBlocks: DefaultEvictionRecommendedMaxBlocks,
		RecommendedMaxBytes:  DefaultEvictionRecommendedMaxBytes,
		MaxValidateSamples:   DefaultEvictionMaxValidateSamples,
	}
}

func ParseEvictionConfigFromEnv() (EvictionConfig, error) {
	cfg := DefaultEvictionConfig()
	var err error

	if cfg.Enabled, err = evictionEnvBool("SCRAP_EVICTION_ENABLED", cfg.Enabled); err != nil {
		return EvictionConfig{}, err
	}
	if cfg.HotResidencyWindow, err = evictionEnvDuration("SCRAP_EVICTION_HOT_RESIDENCY_WINDOW", cfg.HotResidencyWindow); err != nil {
		return EvictionConfig{}, err
	}
	if cfg.PlanTTL, err = evictionEnvDuration("SCRAP_EVICTION_PLAN_TTL", cfg.PlanTTL); err != nil {
		return EvictionConfig{}, err
	}
	if cfg.RecommendedMaxBlocks, err = evictionEnvInt("SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS", cfg.RecommendedMaxBlocks); err != nil {
		return EvictionConfig{}, err
	}
	if cfg.RecommendedMaxBytes, err = evictionEnvInt64("SCRAP_EVICTION_RECOMMENDED_MAX_BYTES", cfg.RecommendedMaxBytes); err != nil {
		return EvictionConfig{}, err
	}
	if cfg.MaxValidateSamples, err = evictionEnvInt("SCRAP_EVICTION_MAX_VALIDATE_SAMPLES", cfg.MaxValidateSamples); err != nil {
		return EvictionConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return EvictionConfig{}, err
	}
	return cfg, nil
}

func (cfg EvictionConfig) withDefaults() EvictionConfig {
	defaults := DefaultEvictionConfig()
	if cfg.PlanTTL == 0 {
		cfg.PlanTTL = defaults.PlanTTL
	}
	if cfg.RecommendedMaxBlocks == 0 {
		cfg.RecommendedMaxBlocks = defaults.RecommendedMaxBlocks
	}
	if cfg.RecommendedMaxBytes == 0 {
		cfg.RecommendedMaxBytes = defaults.RecommendedMaxBytes
	}
	return cfg
}

func (cfg EvictionConfig) Validate() error {
	if cfg.HotResidencyWindow < 0 {
		return fmt.Errorf("invalid SCRAP_EVICTION_HOT_RESIDENCY_WINDOW: %s (must be >= 0)", cfg.HotResidencyWindow)
	}
	if cfg.PlanTTL <= 0 {
		return fmt.Errorf("invalid SCRAP_EVICTION_PLAN_TTL: %s (must be > 0)", cfg.PlanTTL)
	}
	if cfg.RecommendedMaxBlocks <= 0 {
		return fmt.Errorf("invalid SCRAP_EVICTION_RECOMMENDED_MAX_BLOCKS: %d (must be > 0)", cfg.RecommendedMaxBlocks)
	}
	if cfg.RecommendedMaxBytes <= 0 {
		return fmt.Errorf("invalid SCRAP_EVICTION_RECOMMENDED_MAX_BYTES: %d (must be > 0)", cfg.RecommendedMaxBytes)
	}
	if cfg.MaxValidateSamples < 0 {
		return fmt.Errorf("invalid SCRAP_EVICTION_MAX_VALIDATE_SAMPLES: %d (must be >= 0)", cfg.MaxValidateSamples)
	}
	return nil
}

func evictionEnvBool(key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	out, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %q is not a boolean", key, v)
	}
	return out, nil
}

func evictionEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	out, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q is not a duration", key, v)
	}
	return out, nil
}

func evictionEnvInt(key string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	out, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q is not an integer", key, v)
	}
	return out, nil
}

func evictionEnvInt64(key string, fallback int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	out, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q is not an integer", key, v)
	}
	return out, nil
}
