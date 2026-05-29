package scrub

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/ulid"
)

type Proposer interface {
	ProposeConsistencyCheck(ctx context.Context, scrubID string) (Result, error)
}

type ConsistencyChecker interface {
	CheckConsistency(ctx context.Context, addr, scrubID string) (Result, error)
}

type LeaderChecker interface {
	IsLeader() bool
}

type Metrics interface {
	RecordRun(result string, durationSec float64)
}

type Rebuilder interface {
	RequestRebuild(ctx context.Context, addr, scrubID string) error
}

type LightConfig struct {
	Proposer           Proposer
	ConsistencyChecker ConsistencyChecker
	LeaderChecker      LeaderChecker
	Metrics            Metrics
	Rebuilder          Rebuilder
	Logger             *slog.Logger
	PeerAddrs          []string
	Interval           time.Duration
	Jitter             float64
}

type Light struct {
	cfg    LightConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewLight(cfg LightConfig) *Light {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultLightScrubInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Light{cfg: cfg}
}

func (ls *Light) Start(ctx context.Context) {
	ctx, ls.cancel = context.WithCancel(ctx)
	ls.wg.Add(1)
	go ls.loop(ctx)
}

func (ls *Light) Stop() {
	if ls.cancel != nil {
		ls.cancel()
	}
	ls.wg.Wait()
}

func (ls *Light) loop(ctx context.Context) {
	defer ls.wg.Done()

	for {
		delay := ls.jitteredInterval()
		select {
		case <-time.After(delay):
			_ = ls.RunOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (ls *Light) jitteredInterval() time.Duration {
	if ls.cfg.Jitter <= 0 {
		return ls.cfg.Interval
	}
	jitter := ls.cfg.Jitter * float64(ls.cfg.Interval)
	offset := (rand.Float64()*2 - 1) * jitter //nolint:gosec // jitter is not security-sensitive
	return ls.cfg.Interval + time.Duration(offset)
}

func (ls *Light) RunOnce(ctx context.Context) error { //nolint:gocognit // rebuild error handling adds necessary branches
	if !ls.cfg.LeaderChecker.IsLeader() {
		return nil
	}

	start := time.Now()
	scrubID := ulid.New().String()

	leaderResult, err := ls.cfg.Proposer.ProposeConsistencyCheck(ctx, scrubID)
	if err != nil {
		ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
		return err
	}

	var divergent []string
	for _, addr := range ls.cfg.PeerAddrs {
		peerResult, err := ls.cfg.ConsistencyChecker.CheckConsistency(ctx, addr, scrubID)
		if err != nil {
			ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
			return err
		}
		if peerResult.SHA256 != leaderResult.SHA256 {
			divergent = append(divergent, addr)
		}
	}

	duration := time.Since(start).Seconds()
	if len(divergent) > 0 {
		ls.cfg.Metrics.RecordRun("mismatch", duration)
		if ls.cfg.Rebuilder != nil {
			for _, addr := range divergent {
				if err := ls.cfg.Rebuilder.RequestRebuild(ctx, addr, scrubID); err != nil {
					ls.cfg.Logger.WarnContext(ctx, "scrub: rebuild request failed", "addr", addr, "scrub_id", scrubID, "err", err)
				}
			}
		}
	} else {
		ls.cfg.Metrics.RecordRun("ok", duration)
	}
	return nil
}
