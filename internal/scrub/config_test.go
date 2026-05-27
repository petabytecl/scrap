package scrub_test

import (
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
)

func TestParseScrubConfig_Defaults(t *testing.T) {
	cfg := scrub.ParseScrubConfig()

	if !cfg.Enabled {
		t.Error("expected Enabled=true by default")
	}
	if cfg.LightScrubInterval != 24*time.Hour {
		t.Errorf("LightScrubInterval: got %v, want 24h", cfg.LightScrubInterval)
	}
	if cfg.DeepScrubInterval != 168*time.Hour {
		t.Errorf("DeepScrubInterval: got %v, want 168h", cfg.DeepScrubInterval)
	}
	if cfg.DeepScrubIORate != 125_000_000 {
		t.Errorf("DeepScrubIORate: got %d, want 125000000", cfg.DeepScrubIORate)
	}
	if cfg.PauseLatency != 10*time.Millisecond {
		t.Errorf("PauseLatency: got %v, want 10ms", cfg.PauseLatency)
	}
	if cfg.PauseCooldown != 30*time.Second {
		t.Errorf("PauseCooldown: got %v, want 30s", cfg.PauseCooldown)
	}
	if cfg.CorruptCap != 5 {
		t.Errorf("CorruptCap: got %d, want 5", cfg.CorruptCap)
	}
	if cfg.Jitter != 0.1 {
		t.Errorf("Jitter: got %f, want 0.1", cfg.Jitter)
	}
}

func TestParseScrubConfig_EnvOverrides(t *testing.T) {
	t.Setenv("SCRAP_SCRUB_ENABLED", "false")
	t.Setenv("SCRAP_LIGHT_SCRUB_INTERVAL", "12h")
	t.Setenv("SCRAP_DEEP_SCRUB_INTERVAL", "72h")
	t.Setenv("SCRAP_DEEP_SCRUB_IO_RATE", "50000000")
	t.Setenv("SCRAP_SCRUB_PAUSE_LATENCY", "5ms")
	t.Setenv("SCRAP_SCRUB_PAUSE_COOLDOWN", "15s")
	t.Setenv("SCRAP_SCRUB_CORRUPT_CAP", "3")
	t.Setenv("SCRAP_SCRUB_JITTER", "0.05")

	cfg := scrub.ParseScrubConfig()

	if cfg.Enabled {
		t.Error("expected Enabled=false after override")
	}
	if cfg.LightScrubInterval != 12*time.Hour {
		t.Errorf("LightScrubInterval: got %v, want 12h", cfg.LightScrubInterval)
	}
	if cfg.DeepScrubInterval != 72*time.Hour {
		t.Errorf("DeepScrubInterval: got %v, want 72h", cfg.DeepScrubInterval)
	}
	if cfg.DeepScrubIORate != 50_000_000 {
		t.Errorf("DeepScrubIORate: got %d, want 50000000", cfg.DeepScrubIORate)
	}
	if cfg.PauseLatency != 5*time.Millisecond {
		t.Errorf("PauseLatency: got %v, want 5ms", cfg.PauseLatency)
	}
	if cfg.PauseCooldown != 15*time.Second {
		t.Errorf("PauseCooldown: got %v, want 15s", cfg.PauseCooldown)
	}
	if cfg.CorruptCap != 3 {
		t.Errorf("CorruptCap: got %d, want 3", cfg.CorruptCap)
	}
	if cfg.Jitter != 0.05 {
		t.Errorf("Jitter: got %f, want 0.05", cfg.Jitter)
	}
}

func TestParseScrubConfig_ClampsBadValues(t *testing.T) {
	t.Setenv("SCRAP_LIGHT_SCRUB_INTERVAL", "-1h")
	t.Setenv("SCRAP_DEEP_SCRUB_INTERVAL", "0s")
	t.Setenv("SCRAP_DEEP_SCRUB_IO_RATE", "-500")
	t.Setenv("SCRAP_SCRUB_PAUSE_LATENCY", "0s")
	t.Setenv("SCRAP_SCRUB_PAUSE_COOLDOWN", "-10s")
	t.Setenv("SCRAP_SCRUB_CORRUPT_CAP", "0")
	t.Setenv("SCRAP_SCRUB_JITTER", "5.0")

	cfg := scrub.ParseScrubConfig()

	if cfg.LightScrubInterval != 24*time.Hour {
		t.Errorf("LightScrubInterval: got %v, want 24h (clamped)", cfg.LightScrubInterval)
	}
	if cfg.DeepScrubInterval != 168*time.Hour {
		t.Errorf("DeepScrubInterval: got %v, want 168h (clamped)", cfg.DeepScrubInterval)
	}
	if cfg.DeepScrubIORate != 125_000_000 {
		t.Errorf("DeepScrubIORate: got %d, want 125000000 (clamped)", cfg.DeepScrubIORate)
	}
	if cfg.PauseLatency != 10*time.Millisecond {
		t.Errorf("PauseLatency: got %v, want 10ms (clamped)", cfg.PauseLatency)
	}
	if cfg.PauseCooldown != 30*time.Second {
		t.Errorf("PauseCooldown: got %v, want 30s (clamped)", cfg.PauseCooldown)
	}
	if cfg.CorruptCap != 5 {
		t.Errorf("CorruptCap: got %d, want 5 (clamped)", cfg.CorruptCap)
	}
	if cfg.Jitter != 0.1 {
		t.Errorf("Jitter: got %f, want 0.1 (clamped)", cfg.Jitter)
	}
}
