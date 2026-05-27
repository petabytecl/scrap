package scrub

import (
	"context"
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

type ScrubMetrics interface {
	RecordRun(result string, durationSec float64)
}

type LightScrubberConfig struct {
	Proposer           Proposer
	ConsistencyChecker ConsistencyChecker
	LeaderChecker      LeaderChecker
	Metrics            ScrubMetrics
	PeerAddrs          []string
	Interval           time.Duration
	Jitter             float64
}

type LightScrubber struct {
	cfg    LightScrubberConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewLightScrubber(cfg LightScrubberConfig) *LightScrubber {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultLightScrubInterval
	}
	return &LightScrubber{cfg: cfg}
}

func (ls *LightScrubber) Start(ctx context.Context) {
	ctx, ls.cancel = context.WithCancel(ctx)
	ls.wg.Add(1)
	go ls.loop(ctx)
}

func (ls *LightScrubber) Stop() {
	if ls.cancel != nil {
		ls.cancel()
	}
	ls.wg.Wait()
}

func (ls *LightScrubber) loop(ctx context.Context) {
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

func (ls *LightScrubber) jitteredInterval() time.Duration {
	if ls.cfg.Jitter <= 0 {
		return ls.cfg.Interval
	}
	jitter := ls.cfg.Jitter * float64(ls.cfg.Interval)
	offset := (rand.Float64()*2 - 1) * jitter //nolint:gosec // jitter is not security-sensitive
	return ls.cfg.Interval + time.Duration(offset)
}

func (ls *LightScrubber) RunOnce(ctx context.Context) error {
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

	mismatch := false
	for _, addr := range ls.cfg.PeerAddrs {
		peerResult, err := ls.cfg.ConsistencyChecker.CheckConsistency(ctx, addr, scrubID)
		if err != nil {
			ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
			return err
		}
		if peerResult.SHA256 != leaderResult.SHA256 {
			mismatch = true
		}
	}

	duration := time.Since(start).Seconds()
	if mismatch {
		ls.cfg.Metrics.RecordRun("mismatch", duration)
	} else {
		ls.cfg.Metrics.RecordRun("ok", duration)
	}
	return nil
}
