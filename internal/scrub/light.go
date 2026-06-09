package scrub

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/ulid"
)

const (
	defaultPeerResultTimeout      = 10 * time.Second
	defaultPeerResultPollInterval = 200 * time.Millisecond
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
	PeerResultTimeout  time.Duration
	PeerResultPoll     time.Duration
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
	if cfg.PeerResultTimeout <= 0 {
		cfg.PeerResultTimeout = defaultPeerResultTimeout
	}
	if cfg.PeerResultPoll <= 0 {
		cfg.PeerResultPoll = defaultPeerResultPollInterval
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
		ls.cfg.Logger.WarnContext(ctx, "scrub: consistency check proposal failed", "scrub_id", scrubID, "err", err)
		ls.cfg.Metrics.RecordRun("error", time.Since(start).Seconds())
		return err
	}

	var divergent []string
	for _, addr := range ls.cfg.PeerAddrs {
		peerResult, err := ls.checkPeerConsistency(ctx, addr, scrubID)
		if err != nil {
			ls.cfg.Logger.WarnContext(ctx, "scrub: peer consistency check failed", "addr", addr, "scrub_id", scrubID, "err", err)
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

func (ls *Light) checkPeerConsistency(ctx context.Context, addr, scrubID string) (Result, error) {
	deadline := time.NewTimer(ls.cfg.PeerResultTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(ls.cfg.PeerResultPoll)
	defer ticker.Stop()

	var lastErr error
	for {
		result, err := ls.cfg.ConsistencyChecker.CheckConsistency(ctx, addr, scrubID)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrConsistencyResultNotReady) {
			return Result{}, err
		}
		lastErr = err

		select {
		case <-ticker.C:
		case <-deadline.C:
			return Result{}, lastErr
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}
