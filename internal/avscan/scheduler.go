package avscan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const maxSignatureVersionLen = 128

type Config struct {
	ShardID                  uint64
	BlockLister              BlockLister
	LeaderChecker            LeaderChecker
	Engine                   Engine
	ProgressStore            ProgressStore
	SignatureVersionProvider SignatureVersionProvider
	Metrics                  Metrics
	PauseController          PauseController
	IOBudget                 IOBudget
	TickerFactory            TickerFactory
	Interval                 time.Duration
}

type Scheduler struct {
	cfg Config

	runMu     sync.Mutex
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	notify    chan struct{}
	completed map[uint64]struct{}
	snapshot  Snapshot
}

type runProgressState struct {
	enabled          bool
	frontier         uint64
	signatureVersion string
}

type scanRunState struct {
	remaining       int
	frontierBlocked bool
	progress        runProgressState
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
	snapshot := Snapshot{
		Status:      StatusIdle,
		LastReason:  ReasonNone,
		LastUpdated: time.Now(),
	}
	if cfg.Engine == nil {
		snapshot.Status = StatusDegraded
		snapshot.LastReason = ReasonEngineUnavailable
	}
	return &Scheduler{
		cfg:       cfg,
		notify:    make(chan struct{}, 1),
		completed: make(map[uint64]struct{}),
		snapshot:  snapshot,
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

func (s *Scheduler) RunOnce(ctx context.Context) (err error) {
	if s == nil {
		return nil
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	start := time.Now()
	status := StatusIdle
	reason := ReasonNone
	var runErr error
	defer func() {
		s.recordRun(status, reason, time.Since(start))
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			status = StatusDegraded
			reason = ReasonScanPanic
			s.recordFailure(reason, s.lagFromSnapshot())
			err = ErrScanPanic
		}
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
	if s.cfg.Engine == nil {
		status = StatusDegraded
		reason = ReasonEngineUnavailable
		s.recordFailure(reason, s.lagFromSnapshot())
		return ErrEngineUnavailable
	}

	progress, progressReason, err := s.progressForRun(ctx)
	if err != nil {
		status = StatusDegraded
		reason = progressReason
		s.recordFailure(reason, s.lagFromSnapshot())
		return err
	}
	s.updateProgressSnapshot(progress)

	blocks, err := s.cfg.BlockLister.ListSealedBlocks(ctx)
	if err != nil {
		status = StatusDegraded
		reason = ReasonListFailed
		s.recordFailure(reason, s.lagFromSnapshot())
		return fmt.Errorf("avscan: list sealed Blocks: %w", err)
	}
	blocks = s.eligibleBlocks(blocks, progress)
	s.setLag(len(blocks))
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.LagBlocks = len(blocks)
	})

	reason, runErr = s.scanBlocks(ctx, blocks, progress)
	if runErr != nil {
		status = StatusDegraded
		return runErr
	}
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.Status = StatusIdle
		snapshot.LastReason = ReasonNone
		snapshot.LagBlocks = 0
		snapshot.InFlightBlocks = 0
	})
	return nil
}

func (s *Scheduler) scanBlocks(ctx context.Context, blocks []Block, progress runProgressState) (Reason, error) {
	var firstReason Reason
	var firstErr error
	state := scanRunState{
		remaining: len(blocks),
		progress:  progress,
	}
	for _, block := range blocks {
		if reason, err := s.waitBeforeScan(ctx, block, state.remaining); err != nil {
			return reason, err
		}
		if reason, err := s.tryScanBlock(ctx, block, &state); err != nil {
			if firstErr == nil {
				firstReason = reason
				firstErr = err
			}
			if fatalScanReason(reason) {
				return firstReason, firstErr
			}
			continue
		}
	}
	return s.finishScanBlocks(firstReason, firstErr)
}

func (s *Scheduler) tryScanBlock(ctx context.Context, block Block, state *scanRunState) (Reason, error) {
	if err := s.scanOne(ctx, block); err != nil {
		reason := reasonForScanError(err)
		state.frontierBlocked = true
		s.updateLag(state.remaining)
		return reason, err
	}

	state.remaining--
	if reason, err := s.persistProgressAfterScan(ctx, block, state); err != nil {
		return reason, err
	}
	s.updateLag(state.remaining)
	return ReasonNone, nil
}

func (s *Scheduler) persistProgressAfterScan(ctx context.Context, block Block, state *scanRunState) (Reason, error) {
	if !state.progress.enabled || state.frontierBlocked {
		return ReasonNone, nil
	}

	state.progress.frontier = block.BlockID
	if err := s.saveProgress(ctx, state.progress); err != nil {
		reason := ReasonProgressFailed
		s.recordFailure(reason, state.remaining)
		return reason, err
	}
	return ReasonNone, nil
}

func (s *Scheduler) waitBeforeScan(ctx context.Context, block Block, remaining int) (Reason, error) {
	if err := ctx.Err(); err != nil {
		reason := ReasonCanceled
		s.recordFailure(reason, remaining)
		return reason, err
	}
	if err := s.waitPause(ctx); err != nil {
		reason := reasonForWaitError(err, ReasonPaused)
		s.recordFailure(reason, remaining)
		return reason, err
	}
	if err := s.waitBudget(ctx, block); err != nil {
		reason := reasonForWaitError(err, ReasonIOBudget)
		s.recordFailure(reason, remaining)
		return reason, err
	}
	return ReasonNone, nil
}

func (s *Scheduler) finishScanBlocks(firstReason Reason, firstErr error) (Reason, error) {
	if firstErr == nil {
		return ReasonNone, nil
	}
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.Status = StatusDegraded
		snapshot.LastReason = firstReason
		snapshot.InFlightBlocks = 0
	})
	return firstReason, firstErr
}

func fatalScanReason(reason Reason) bool {
	return reason == ReasonCanceled || reason == ReasonEngineUnavailable
}

func (s *Scheduler) loop(ctx context.Context, done chan struct{}) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.recordFailure(ReasonScanPanic, s.lagFromSnapshot())
		}
		close(done)
	}()

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

func (s *Scheduler) eligibleBlocks(blocks []Block, progress runProgressState) []Block {
	ordered := append([]Block(nil), blocks...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].BlockID < ordered[j].BlockID
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Block, 0, len(ordered))
	for _, block := range ordered {
		if progress.enabled && block.BlockID <= progress.frontier {
			continue
		}
		if _, ok := s.completed[block.BlockID]; ok {
			s.recordDuplicate()
			continue
		}
		out = append(out, block)
	}
	return out
}

func (s *Scheduler) progressForRun(ctx context.Context) (runProgressState, Reason, error) {
	if s.cfg.ProgressStore == nil {
		return runProgressState{}, ReasonNone, nil
	}

	progress, err := s.loadProgress(ctx)
	if err != nil {
		return runProgressState{}, ReasonProgressFailed, err
	}
	if err := validateSignatureVersion(progress.LastSignatureVersionScanned); err != nil {
		return runProgressState{}, ReasonProgressFailed, err
	}

	progress, err = s.progressWithCurrentSignature(ctx, progress)
	if err != nil {
		return runProgressState{}, ReasonProgressFailed, err
	}

	return runProgressState{
		enabled:          true,
		frontier:         progress.LastScannedBlockID,
		signatureVersion: progress.LastSignatureVersionScanned,
	}, ReasonNone, nil
}

func (s *Scheduler) loadProgress(ctx context.Context) (Progress, error) {
	progress, err := s.cfg.ProgressStore.LoadScannerProgress(ctx)
	if err == nil {
		return progress, nil
	}
	if errors.Is(err, ErrProgressNotFound) {
		return Progress{}, nil
	}
	return Progress{}, fmt.Errorf("avscan: load scanner progress: %w", err)
}

func (s *Scheduler) progressWithCurrentSignature(ctx context.Context, progress Progress) (Progress, error) {
	if s.cfg.SignatureVersionProvider == nil {
		return progress, nil
	}

	current, err := s.cfg.SignatureVersionProvider.SignatureVersion(ctx)
	if err != nil {
		return Progress{}, fmt.Errorf("avscan: signature version: %w", err)
	}
	if err := validateSignatureVersion(current); err != nil {
		return Progress{}, err
	}
	if current == progress.LastSignatureVersionScanned {
		return progress, nil
	}

	reset := Progress{
		LastScannedBlockID:          0,
		LastSignatureVersionScanned: current,
	}
	if err := s.cfg.ProgressStore.SaveScannerProgress(ctx, reset); err != nil {
		return Progress{}, fmt.Errorf("avscan: reset scanner progress: %w", err)
	}
	return reset, nil
}

func (s *Scheduler) updateProgressSnapshot(progress runProgressState) {
	if !progress.enabled {
		return
	}
	s.updateSnapshot(func(snapshot *Snapshot) {
		if progress.frontier > snapshot.LastScannedBlockID {
			snapshot.LastScannedBlockID = progress.frontier
		}
	})
}

func (s *Scheduler) saveProgress(ctx context.Context, progress runProgressState) error {
	if s.cfg.ProgressStore == nil {
		return nil
	}
	err := s.cfg.ProgressStore.SaveScannerProgress(ctx, Progress{
		LastScannedBlockID:          progress.frontier,
		LastSignatureVersionScanned: progress.signatureVersion,
	})
	if err != nil {
		return fmt.Errorf("avscan: save scanner progress: %w", err)
	}
	return nil
}

func validateSignatureVersion(version string) error {
	if len(version) > maxSignatureVersionLen {
		return fmt.Errorf("avscan: signature version length %d", len(version))
	}
	for i := range version {
		if !isSignatureVersionByte(version[i]) {
			return fmt.Errorf("avscan: signature version contains invalid byte at %d", i)
		}
	}
	return nil
}

func isSignatureVersionByte(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '.', '-', '_', ':':
		return true
	default:
		return false
	}
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
		if block.BlockID > snapshot.LastScannedBlockID {
			snapshot.LastScannedBlockID = block.BlockID
		}
		snapshot.ScannedBlocks++
		snapshot.InFlightBlocks = 0
		s.completed[block.BlockID] = struct{}{}
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
	s.withMetrics(func(metrics Metrics) {
		metrics.RecordRun(s.cfg.ShardID, string(status), string(reason), duration)
	})
}

func (s *Scheduler) recordBlock(status string, reason Reason) {
	s.withMetrics(func(metrics Metrics) {
		metrics.RecordBlock(s.cfg.ShardID, status, string(reason))
	})
}

func (s *Scheduler) recordMetricsFailure(reason Reason) {
	s.withMetrics(func(metrics Metrics) {
		metrics.RecordFailure(s.cfg.ShardID, string(reason))
	})
}

func (s *Scheduler) setLag(blocks int) {
	s.withMetrics(func(metrics Metrics) {
		metrics.SetLag(s.cfg.ShardID, blocks)
	})
}

func (s *Scheduler) setInFlight(blocks int) {
	s.withMetrics(func(metrics Metrics) {
		metrics.SetInFlight(s.cfg.ShardID, blocks)
	})
}

func (s *Scheduler) recordDuplicate() {
	s.withMetrics(func(metrics Metrics) {
		metrics.RecordDuplicate(s.cfg.ShardID)
	})
}

func (s *Scheduler) updateLag(blocks int) {
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.LagBlocks = blocks
	})
	s.setLag(blocks)
}

func (s *Scheduler) withMetrics(record func(Metrics)) {
	if s.cfg.Metrics == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	record(s.cfg.Metrics)
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
