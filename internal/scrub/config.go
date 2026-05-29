package scrub

import (
	"os"
	"strconv"
	"time"
)

const (
	DefaultLightScrubInterval = 24 * time.Hour
	DefaultDeepScrubInterval  = 168 * time.Hour
	DefaultDeepScrubIORate    = 125_000_000
	DefaultPauseLatency       = 10 * time.Millisecond
	DefaultPauseCooldown      = 30 * time.Second
	DefaultCorruptCap         = 5
	DefaultJitter             = 0.1
	maxJitter                 = 1.0
)

type Config struct {
	Enabled            bool
	LightScrubInterval time.Duration
	DeepScrubInterval  time.Duration
	DeepScrubIORate    int64
	PauseLatency       time.Duration
	PauseCooldown      time.Duration
	CorruptCap         int
	Jitter             float64
}

func ParseConfig() Config {
	cfg := Config{
		Enabled:            envBool("SCRAP_SCRUB_ENABLED", true),
		LightScrubInterval: envDuration("SCRAP_LIGHT_SCRUB_INTERVAL", DefaultLightScrubInterval),
		DeepScrubInterval:  envDuration("SCRAP_DEEP_SCRUB_INTERVAL", DefaultDeepScrubInterval),
		DeepScrubIORate:    envInt64("SCRAP_DEEP_SCRUB_IO_RATE", DefaultDeepScrubIORate),
		PauseLatency:       envDuration("SCRAP_SCRUB_PAUSE_LATENCY", DefaultPauseLatency),
		PauseCooldown:      envDuration("SCRAP_SCRUB_PAUSE_COOLDOWN", DefaultPauseCooldown),
		CorruptCap:         envInt("SCRAP_SCRUB_CORRUPT_CAP", DefaultCorruptCap),
		Jitter:             envFloat64("SCRAP_SCRUB_JITTER", DefaultJitter),
	}
	clampScrubConfig(&cfg)
	return cfg
}

func clampScrubConfig(cfg *Config) {
	if cfg.LightScrubInterval <= 0 {
		cfg.LightScrubInterval = DefaultLightScrubInterval
	}
	if cfg.DeepScrubInterval <= 0 {
		cfg.DeepScrubInterval = DefaultDeepScrubInterval
	}
	if cfg.DeepScrubIORate <= 0 {
		cfg.DeepScrubIORate = DefaultDeepScrubIORate
	}
	if cfg.PauseLatency <= 0 {
		cfg.PauseLatency = DefaultPauseLatency
	}
	if cfg.PauseCooldown <= 0 {
		cfg.PauseCooldown = DefaultPauseCooldown
	}
	if cfg.CorruptCap <= 0 {
		cfg.CorruptCap = DefaultCorruptCap
	}
	if cfg.Jitter < 0 || cfg.Jitter > maxJitter {
		cfg.Jitter = DefaultJitter
	}
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat64(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
