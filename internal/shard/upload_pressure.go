package shard

import (
	"context"
	"os"
	"strconv"
	"sync"
)

const (
	DefaultUploadBudgetBytes int64 = 10 * 1024 * 1024 * 1024
	DefaultUploadWarnPct           = 0.80
	DefaultUploadPressurePct       = 0.90
	DefaultUploadCriticalPct       = 0.95

	uploadPressureConcurrencyMultiplier = 2
)

type UploadPressureLevel int

const (
	UploadPressureLevelOK UploadPressureLevel = iota
	UploadPressureLevelWarn
	UploadPressureLevelPressure
	UploadPressureLevelCritical
)

type UploadPressureConfig struct {
	BudgetBytes int64
	WarnPct     float64
	PressurePct float64
	CriticalPct float64
}

type UploadPressureSnapshot struct {
	Level         UploadPressureLevel
	PendingBytes  int64
	PendingBlocks int
}

func ParseUploadPressureConfigFromEnv() UploadPressureConfig {
	return normalizeUploadPressureConfig(UploadPressureConfig{
		BudgetBytes: envInt64("SCRAP_UPLOAD_BUDGET", DefaultUploadBudgetBytes),
		WarnPct:     envFloat64("SCRAP_UPLOAD_WARN_PCT", DefaultUploadWarnPct),
		PressurePct: envFloat64("SCRAP_UPLOAD_PRESSURE_PCT", DefaultUploadPressurePct),
		CriticalPct: envFloat64("SCRAP_UPLOAD_CRITICAL_PCT", DefaultUploadCriticalPct),
	})
}

func (l UploadPressureLevel) String() string {
	switch l {
	case UploadPressureLevelOK:
		return "ok"
	case UploadPressureLevelWarn:
		return "warn"
	case UploadPressureLevelPressure:
		return "pressure"
	case UploadPressureLevelCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func (s *Shard) UploadPressureForTest() UploadPressureSnapshot {
	return s.uploads.snapshot()
}

func (s *Shard) UploadConcurrencyForTest() int {
	return s.uploads.concurrency()
}

func (s *Shard) DeepScrubPausedForTest() bool {
	if s.uploadPressureScrubGate == nil {
		return false
	}
	return s.uploadPressureScrubGate.IsPaused()
}

func (s *Shard) UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int) {
	snapshot := s.uploads.snapshot()
	return int(snapshot.Level), snapshot.Level.String(), snapshot.PendingBytes, snapshot.PendingBlocks
}

// refreshUploadPressureLocked reads the pending-upload outbox under s.mu and
// pushes the resulting stats to the upload controller (the "pressure push" seam).
func (s *Shard) refreshUploadPressureLocked() error {
	uploads, err := collectPendingUploads(s.idx)
	if err != nil {
		return err
	}
	s.uploads.SetPressure(s.uploadObligations.pressureStats(uploads))
	return nil
}

func (cfg UploadPressureConfig) levelFor(pendingBytes int64) UploadPressureLevel {
	cfg = normalizeUploadPressureConfig(cfg)
	used := float64(pendingBytes)
	budget := float64(cfg.BudgetBytes)

	switch {
	case used > budget*cfg.CriticalPct:
		return UploadPressureLevelCritical
	case used > budget*cfg.PressurePct:
		return UploadPressureLevelPressure
	case used > budget*cfg.WarnPct:
		return UploadPressureLevelWarn
	default:
		return UploadPressureLevelOK
	}
}

func normalizeUploadPressureConfig(cfg UploadPressureConfig) UploadPressureConfig {
	defaults := UploadPressureConfig{
		BudgetBytes: DefaultUploadBudgetBytes,
		WarnPct:     DefaultUploadWarnPct,
		PressurePct: DefaultUploadPressurePct,
		CriticalPct: DefaultUploadCriticalPct,
	}
	if cfg.BudgetBytes <= 0 {
		cfg.BudgetBytes = defaults.BudgetBytes
	}
	if !validPct(cfg.WarnPct) || !validPct(cfg.PressurePct) || !validPct(cfg.CriticalPct) {
		cfg.WarnPct = defaults.WarnPct
		cfg.PressurePct = defaults.PressurePct
		cfg.CriticalPct = defaults.CriticalPct
	}
	if !(cfg.WarnPct < cfg.PressurePct && cfg.PressurePct < cfg.CriticalPct) {
		cfg.WarnPct = defaults.WarnPct
		cfg.PressurePct = defaults.PressurePct
		cfg.CriticalPct = defaults.CriticalPct
	}
	return cfg
}

func validPct(v float64) bool {
	return v > 0 && v <= 1
}

type pressurePauseGate struct {
	mu     sync.Mutex
	paused bool
	resume chan struct{}
}

func newPressurePauseGate() *pressurePauseGate {
	return &pressurePauseGate{resume: make(chan struct{})}
}

func (g *pressurePauseGate) IsPaused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.paused
}

func (g *pressurePauseGate) SetPaused(paused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if paused == g.paused {
		return
	}
	g.paused = paused
	if paused {
		g.resume = make(chan struct{})
		return
	}
	close(g.resume)
}

func (g *pressurePauseGate) Wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		if !g.paused {
			g.mu.Unlock()
			return nil
		}
		resume := g.resume
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resume:
		}
	}
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

func envFloat64(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}
