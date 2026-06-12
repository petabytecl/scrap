package avscan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Config struct {
	ShardID         uint64
	BlockLister     BlockLister
	LeaderChecker   LeaderChecker
	Engine          Engine
	Metrics         Metrics
	PauseController PauseController
	IOBudget        IOBudget
	TickerFactory   TickerFactory
	Interval        time.Duration
}

type Scheduler struct {
	cfg Config

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	notify   chan struct{}
	snapshot Snapshot
}

type realTickerFactory struct{}

func (realTickerFactory) NewTicker(interval time.Duration) Ticker {
	return &realTicker{ticker: time.NewTicker(interval)}
}

type realTicker struct {
	ticker *time.Ticker
}

func (t *realTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t *realTicker) Stop() {
	t.ticker.Stop()
}

func NewScheduler(cfg Config) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.TickerFactory == nil {
		cfg.TickerFactory = realTickerFactory{}
	}
	return &Scheduler{
		cfg:    cfg,
		notify: make(chan struct{}, 1),
		snapshot: Snapshot{
			Status:      StatusIdle,
			LastReason:  ReasonNone,
			LastUpdated: time.Now(),
		},
	}
}

func (s *Scheduler) Start(parent context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	go s.loop(ctx, done)
}

func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *Scheduler) Notify() {
	if s == nil || s.notify == nil {
		return
	}
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{Status: StatusIdle, LastReason: ReasonNone}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}
	start := time.Now()
	status := StatusIdle
	reason := ReasonNone
	var runErr error
	defer func() {
		s.recordRun(status, reason, time.Since(start))
	}()

	if s.cfg.LeaderChecker != nil && !s.cfg.LeaderChecker.IsLeader() {
		reason = ReasonNotLeader
		s.updateSnapshot(func(snapshot *Snapshot) {
			snapshot.Status = StatusIdle
			snapshot.LastReason = ReasonNotLeader
			snapshot.LagBlocks = 0
			snapshot.InFlightBlocks = 0
		})
		s.setLag(0)
		return nil
	}
	if s.cfg.BlockLister == nil {
		return nil
	}

	blocks, err := s.cfg.BlockLister.ListSealedBlocks(ctx)
	if err != nil {
		status = StatusDegraded
		reason = ReasonListFailed
		s.recordFailure(reason, s.lagFromSnapshot())
		return fmt.Errorf("avscan: list sealed Blocks: %w", err)
	}
	blocks = s.eligibleBlocks(blocks)
	s.setLag(len(blocks))
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.LagBlocks = len(blocks)
	})

	reason, runErr = s.scanBlocks(ctx, blocks)
	if runErr != nil {
		status = StatusDegraded
		return runErr
	}
	s.setLag(0)
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.Status = StatusIdle
		snapshot.LastReason = ReasonNone
		snapshot.LagBlocks = 0
		snapshot.InFlightBlocks = 0
	})
	return nil
}

func (s *Scheduler) scanBlocks(ctx context.Context, blocks []Block) (Reason, error) {
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			reason := ReasonCanceled
			s.recordFailure(reason, len(blocks))
			return reason, err
		}
		if err := s.waitPause(ctx); err != nil {
			reason := reasonForWaitError(err, ReasonPaused)
			s.recordFailure(reason, len(blocks))
			return reason, err
		}
		if err := s.waitBudget(ctx, block); err != nil {
			reason := reasonForWaitError(err, ReasonIOBudget)
			s.recordFailure(reason, len(blocks))
			return reason, err
		}
		if err := s.scanOne(ctx, block); err != nil {
			return reasonForScanError(err), err
		}
	}
	return ReasonNone, nil
}

func (s *Scheduler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)

	ticker := s.cfg.TickerFactory.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.notify:
			_ = s.RunOnce(ctx)
		case <-ticker.C():
			_ = s.RunOnce(ctx)
		}
	}
}

func (s *Scheduler) eligibleBlocks(blocks []Block) []Block {
	s.mu.Lock()
	lastScanned := s.snapshot.LastScannedBlockID
	s.mu.Unlock()

	out := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		if block.BlockID <= lastScanned {
			s.recordDuplicate()
			continue
		}
		out = append(out, block)
	}
	return out
}

func (s *Scheduler) waitPause(ctx context.Context) error {
	if s.cfg.PauseController == nil || !s.cfg.PauseController.IsPaused() {
		return nil
	}
	return s.cfg.PauseController.Wait(ctx)
}

func (s *Scheduler) waitBudget(ctx context.Context, block Block) error {
	if s.cfg.IOBudget == nil {
		return nil
	}
	return s.cfg.IOBudget.Wait(ctx, block.SizeBytes)
}

func (s *Scheduler) scanOne(ctx context.Context, block Block) error {
	if s.cfg.Engine == nil {
		return nil
	}
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.Status = StatusScanning
		snapshot.LastReason = ReasonNone
		snapshot.InFlightBlocks = 1
	})
	s.setInFlight(1)
	result, err := s.safeScan(ctx, block)
	s.setInFlight(0)
	if err != nil {
		reason := reasonForScanError(err)
		s.updateSnapshot(func(snapshot *Snapshot) {
			snapshot.Status = StatusDegraded
			snapshot.LastReason = reason
			snapshot.InFlightBlocks = 0
			snapshot.FailedBlocks++
		})
		s.recordBlock("failed", reason)
		s.recordMetricsFailure(reason)
		return err
	}
	s.recordBlock(string(result.Status), ReasonNone)
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.LastScannedBlockID = block.BlockID
		snapshot.ScannedBlocks++
		snapshot.InFlightBlocks = 0
	})
	return nil
}

func (s *Scheduler) safeScan(ctx context.Context, block Block) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w", ErrScanPanic)
		}
	}()
	return s.cfg.Engine.Scan(ctx, block)
}

func (s *Scheduler) updateSnapshot(update func(*Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	update(&s.snapshot)
	s.snapshot.LastUpdated = time.Now()
}

func (s *Scheduler) recordFailure(reason Reason, lagBlocks int) {
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.Status = StatusDegraded
		snapshot.LastReason = reason
		snapshot.LagBlocks = lagBlocks
		snapshot.InFlightBlocks = 0
		snapshot.FailedBlocks++
	})
	s.recordMetricsFailure(reason)
	s.setInFlight(0)
}

func (s *Scheduler) lagFromSnapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.LagBlocks
}

func (s *Scheduler) recordRun(status Status, reason Reason, duration time.Duration) {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.RecordRun(s.cfg.ShardID, string(status), string(reason), duration)
	}
}

func (s *Scheduler) recordBlock(status string, reason Reason) {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.RecordBlock(s.cfg.ShardID, status, string(reason))
	}
}

func (s *Scheduler) recordMetricsFailure(reason Reason) {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.RecordFailure(s.cfg.ShardID, string(reason))
	}
}

func (s *Scheduler) setLag(blocks int) {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.SetLag(s.cfg.ShardID, blocks)
	}
}

func (s *Scheduler) setInFlight(blocks int) {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.SetInFlight(s.cfg.ShardID, blocks)
	}
}

func (s *Scheduler) recordDuplicate() {
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.RecordDuplicate(s.cfg.ShardID)
	}
}

func reasonForWaitError(err error, fallback Reason) Reason {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ReasonCanceled
	}
	return fallback
}

func reasonForScanError(err error) Reason {
	switch {
	case errors.Is(err, ErrEngineUnavailable):
		return ReasonEngineUnavailable
	case errors.Is(err, ErrScanPanic):
		return ReasonScanPanic
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ReasonCanceled
	default:
		return ReasonScanFailed
	}
}
