package shard

import (
	"context"
	"fmt"
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

func ParseUploadPressureConfigFromEnv() (UploadPressureConfig, error) {
	budget, err := envInt64("SCRAP_UPLOAD_BUDGET", DefaultUploadBudgetBytes)
	if err != nil {
		return UploadPressureConfig{}, err
	}
	if budget <= 0 {
		return UploadPressureConfig{}, fmt.Errorf("shard: invalid SCRAP_UPLOAD_BUDGET: %d must be positive", budget)
	}
	warn, err := envPct("SCRAP_UPLOAD_WARN_PCT", DefaultUploadWarnPct)
	if err != nil {
		return UploadPressureConfig{}, err
	}
	pressure, err := envPct("SCRAP_UPLOAD_PRESSURE_PCT", DefaultUploadPressurePct)
	if err != nil {
		return UploadPressureConfig{}, err
	}
	critical, err := envPct("SCRAP_UPLOAD_CRITICAL_PCT", DefaultUploadCriticalPct)
	if err != nil {
		return UploadPressureConfig{}, err
	}
	if !(warn < pressure && pressure < critical) {
		return UploadPressureConfig{}, fmt.Errorf(
			"shard: SCRAP_UPLOAD_WARN_PCT (%v) < SCRAP_UPLOAD_PRESSURE_PCT (%v) < SCRAP_UPLOAD_CRITICAL_PCT (%v) ordering violated",
			warn, pressure, critical,
		)
	}
	return UploadPressureConfig{
		BudgetBytes: budget,
		WarnPct:     warn,
		PressurePct: pressure,
		CriticalPct: critical,
	}, nil
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
	if s.uploadPressure == nil {
		return false
	}
	return s.uploadPressure.DeepScrubPaused()
}

func (s *Shard) UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int) {
	snapshot := s.uploads.snapshot()
	return int(snapshot.Level), snapshot.Level.String(), snapshot.PendingBytes, snapshot.PendingBlocks
}

// refreshUploadPressureLocked reads the pending-upload outbox under s.mu and
// pushes the resulting stats to the upload controller (the "pressure push" seam).
func (s *Shard) refreshUploadPressureLocked() error {
	return s.uploadOutboxLocked().RefreshPressure(s.uploads)
}

type uploadPressureCoordinator struct {
	scrubGate *pressurePauseGate
}

func newUploadPressureCoordinator() *uploadPressureCoordinator {
	return &uploadPressureCoordinator{scrubGate: newPressurePauseGate()}
}

func (c *uploadPressureCoordinator) ScrubPauseController() *pressurePauseGate {
	if c == nil {
		return nil
	}
	return c.scrubGate
}

func (c *uploadPressureCoordinator) ApplyPressureLevel(level UploadPressureLevel) {
	if c == nil || c.scrubGate == nil {
		return
	}
	c.scrubGate.SetPaused(level == UploadPressureLevelCritical)
}

func (c *uploadPressureCoordinator) DeepScrubPaused() bool {
	if c == nil || c.scrubGate == nil {
		return false
	}
	return c.scrubGate.IsPaused()
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

func envInt64(key string, fallback int64) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("shard: invalid %s: %w", key, err)
	}
	return n, nil
}

// envPct reads a fractional threshold in (0, 1]. Malformed or out-of-range
// explicit input is a startup error naming the key, never a silent fallback.
func envPct(key string, fallback float64) (float64, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("shard: invalid %s: %w", key, err)
	}
	if !validPct(n) {
		return 0, fmt.Errorf("shard: invalid %s: %v is not in (0, 1]", key, n)
	}
	return n, nil
}
