package avscan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	storeapi "github.com/petabytecl/scrap/internal/store"
)

const maxSignatureVersionLen = 128

type Config struct {
	ShardID                  uint64
	BlockLister              BlockLister
	LeaderChecker            LeaderChecker
	Engine                   Engine
	ProgressStore            ProgressStore
	SignatureVersionProvider SignatureVersionProvider
	DetectionReporter        DetectionReporter
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
	enabled                   bool
	frontier                  uint64
	signatureVersion          string
	resetDuplicateSuppression bool
}

type scanRunState struct {
	remaining int
	progress  runProgressState
	// listed and restored describe the current sealed listing: the frontier
	// may advance through IDs absent from listed (quarantined or evicted
	// Blocks that no longer exist locally), while restored Blocks keep their
	// completed entries so an in-process rescan loop cannot form.
	listed    map[uint64]struct{}
	restored  map[uint64]struct{}
	maxListed uint64
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

	progress, listing, progressReason, err := s.prepareScanRun(ctx)
	if err != nil {
		status = StatusDegraded
		reason = progressReason
		s.recordFailure(reason, s.lagFromSnapshot())
		return err
	}

	blocks := s.eligibleBlocks(listing, progress)
	s.setLag(len(blocks))
	s.updateSnapshot(func(snapshot *Snapshot) {
		snapshot.LagBlocks = len(blocks)
	})

	reason, runErr = s.scanBlocks(ctx, blocks, listing, progress)
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

func (s *Scheduler) prepareScanRun(ctx context.Context) (runProgressState, []Block, Reason, error) {
	progress, reason, err := s.progressForRun(ctx)
	if err != nil {
		return runProgressState{}, nil, reason, err
	}
	if progress.resetDuplicateSuppression {
		s.clearCompleted()
		s.updateProgressSnapshot(progress)
	}

	blocks, err := s.cfg.BlockLister.ListSealedBlocks(ctx)
	if err != nil {
		return runProgressState{}, nil, ReasonListFailed, fmt.Errorf("avscan: list sealed Blocks: %w", err)
	}
	progress, reason, err = s.reconcileProgressWithBlocks(ctx, progress, blocks)
	if err != nil {
		return runProgressState{}, nil, reason, err
	}
	if progress.resetDuplicateSuppression {
		s.clearCompleted()
	}
	s.updateProgressSnapshot(progress)
	return progress, blocks, ReasonNone, nil
}

func (s *Scheduler) scanBlocks(ctx context.Context, blocks, listing []Block, progress runProgressState) (Reason, error) {
	var firstReason Reason
	var firstErr error
	state := newScanRunState(blocks, listing, progress)
	// Advance through leading gaps before scanning: a quarantined or evicted
	// Block at frontier+1 must not pin the durable watermark when everything
	// still listed is already completed or absent.
	if reason, err := s.persistAdvancedProgress(ctx, &state); err != nil {
		s.recordFailure(reason, state.remaining)
		return reason, err
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

func newScanRunState(blocks, listing []Block, progress runProgressState) scanRunState {
	state := scanRunState{
		remaining: len(blocks),
		progress:  progress,
		listed:    make(map[uint64]struct{}, len(listing)),
		restored:  make(map[uint64]struct{}),
	}
	for _, block := range listing {
		state.listed[block.BlockID] = struct{}{}
		if block.Restored {
			state.restored[block.BlockID] = struct{}{}
		}
		if block.BlockID > state.maxListed {
			state.maxListed = block.BlockID
		}
	}
	return state
}

func (s *Scheduler) tryScanBlock(ctx context.Context, block Block, state *scanRunState) (Reason, error) {
	if err := s.scanOne(ctx, block); err != nil {
		reason := reasonForScanError(err)
		s.updateLag(state.remaining)
		return reason, err
	}

	state.remaining--
	if reason, err := s.persistAdvancedProgress(ctx, state); err != nil {
		s.recordFailure(reason, state.remaining)
		return reason, err
	}
	s.updateLag(state.remaining)
	return ReasonNone, nil
}

func (s *Scheduler) persistAdvancedProgress(ctx context.Context, state *scanRunState) (Reason, error) {
	if !state.progress.enabled {
		return ReasonNone, nil
	}
	before := state.progress.frontier
	s.advanceProgressFrontier(state)
	if state.progress.frontier == before {
		return ReasonNone, nil
	}
	if err := s.saveProgress(ctx, state.progress); err != nil {
		return reasonForProgressError(err), err
	}
	return ReasonNone, nil
}

// advanceProgressFrontier moves the durable frontier through Block IDs that
// are either completed in this process or absent from the current sealed
// listing (quarantined or evicted Blocks that may never reappear). It stops
// at the first listed Block that has not been scanned — a scan failure there
// keeps the watermark below it — and prunes completed entries at or below
// the new frontier, except restored Blocks whose entries suppress in-process
// rescans while they remain scan-eligible below the frontier.
func (s *Scheduler) advanceProgressFrontier(state *scanRunState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	progress := &state.progress
	for progress.frontier < state.maxListed {
		next := progress.frontier + 1
		if _, done := s.completed[next]; !done {
			if _, isListed := state.listed[next]; isListed {
				break
			}
		}
		progress.frontier = next
	}
	for blockID := range s.completed {
		if blockID > progress.frontier {
			continue
		}
		if _, isRestored := state.restored[blockID]; isRestored {
			continue
		}
		delete(s.completed, blockID)
	}
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
	seen := make(map[uint64]struct{}, len(ordered))
	for _, block := range ordered {
		if _, ok := seen[block.BlockID]; ok {
			s.recordDuplicate()
			continue
		}
		seen[block.BlockID] = struct{}{}
		// Restored Blocks stay eligible below the frontier: they may have
		// been evicted before their first scan and the frontier may have
		// advanced through their gap while they were absent.
		if progress.enabled && block.BlockID <= progress.frontier && !block.Restored {
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
	if s.cfg.ProgressStore == nil || s.cfg.SignatureVersionProvider == nil {
		return runProgressState{}, ReasonNone, nil
	}

	progress, err := s.loadProgress(ctx)
	if err != nil {
		return runProgressState{}, reasonForProgressError(err), err
	}
	if err := validateSignatureVersion(progress.LastSignatureVersionScanned); err != nil {
		return runProgressState{}, ReasonProgressFailed, err
	}

	progress, changed, err := s.progressWithCurrentSignature(ctx, progress)
	if err != nil {
		return runProgressState{}, reasonForProgressError(err), err
	}

	return runProgressState{
		enabled:                   true,
		frontier:                  progress.LastScannedBlockID,
		signatureVersion:          progress.LastSignatureVersionScanned,
		resetDuplicateSuppression: changed,
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

func (s *Scheduler) progressWithCurrentSignature(ctx context.Context, progress Progress) (Progress, bool, error) {
	current, err := s.cfg.SignatureVersionProvider.SignatureVersion(ctx)
	if err != nil {
		return Progress{}, false, fmt.Errorf("avscan: signature version: %w", err)
	}
	if err := validateSignatureVersion(current); err != nil {
		return Progress{}, false, err
	}
	if current == progress.LastSignatureVersionScanned {
		return progress, false, nil
	}

	reset := Progress{
		LastScannedBlockID:          0,
		LastSignatureVersionScanned: current,
	}
	if err := s.cfg.ProgressStore.SaveScannerProgress(ctx, reset); err != nil {
		return Progress{}, false, fmt.Errorf("avscan: reset scanner progress: %w", err)
	}
	return reset, true, nil
}

func (s *Scheduler) reconcileProgressWithBlocks(
	ctx context.Context,
	progress runProgressState,
	blocks []Block,
) (runProgressState, Reason, error) {
	if !progress.enabled || len(blocks) == 0 {
		return progress, ReasonNone, nil
	}
	maxBlockID := maxListedBlockID(blocks)
	if progress.frontier <= maxBlockID {
		return progress, ReasonNone, nil
	}

	progress.frontier = 0
	progress.resetDuplicateSuppression = true
	if err := s.saveProgress(ctx, progress); err != nil {
		return runProgressState{}, reasonForProgressError(err), err
	}
	return progress, ReasonNone, nil
}

func maxListedBlockID(blocks []Block) uint64 {
	var maxBlockID uint64
	for _, block := range blocks {
		if block.BlockID > maxBlockID {
			maxBlockID = block.BlockID
		}
	}
	return maxBlockID
}

func (s *Scheduler) updateProgressSnapshot(progress runProgressState) {
	if !progress.enabled {
		return
	}
	s.updateSnapshot(func(snapshot *Snapshot) {
		if progress.resetDuplicateSuppression || progress.frontier > snapshot.LastScannedBlockID {
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

func (s *Scheduler) clearCompleted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = make(map[uint64]struct{})
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
	if err := s.reportDetections(ctx, block, result); err != nil {
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

func (s *Scheduler) reportDetections(ctx context.Context, block Block, result Result) error {
	if err := validateDetectionResult(result); err != nil {
		return err
	}
	if len(result.Detections) == 0 {
		return nil
	}
	if s.cfg.DetectionReporter == nil {
		return ErrDetectionReporterUnavailable
	}
	// MaxDetectionsPerBlock bounds the size of a single quarantine report, not
	// the number of detections a Block may carry: report in chunks so a Block
	// whose detection count exceeds the cap still quarantines every hit instead
	// of being rejected wholesale (which would leave known malware served and
	// wedge the scan frontier on that Block forever).
	for start := 0; start < len(result.Detections); start += MaxDetectionsPerBlock {
		end := min(start+MaxDetectionsPerBlock, len(result.Detections))
		batch := append([]Detection(nil), result.Detections[start:end]...)
		if err := s.cfg.DetectionReporter.ReportDetections(ctx, block, batch); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("%w: %w", ErrQuarantineFailed, err)
		}
	}
	return nil
}

func validateDetectionResult(result Result) error {
	if len(result.Detections) == 0 {
		if result.Status == ResultDetected {
			return fmt.Errorf("%w: detected result without detections", ErrInvalidDetection)
		}
		return nil
	}
	if result.Status != ResultDetected {
		return fmt.Errorf("%w: detections require detected result", ErrInvalidDetection)
	}
	for _, detection := range result.Detections {
		if err := validateDetection(detection); err != nil {
			return err
		}
	}
	return nil
}

func validateDetection(detection Detection) error {
	if err := storeapi.ValidateDocumentIdentity(detection.TransactionID, detection.DocumentName, ""); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDetection, err)
	}
	if detection.DetectedAtUs <= 0 {
		return fmt.Errorf("%w: detected_at_us is required", ErrInvalidDetection)
	}
	if detection.ScanType != DetectionScanTypeInitial && detection.ScanType != DetectionScanTypeRescan {
		return fmt.Errorf("%w: scan_type is required", ErrInvalidDetection)
	}
	if detection.Reason != DetectionReasonScannerDetection {
		return fmt.Errorf("%w: reason is required", ErrInvalidDetection)
	}
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
	case errors.Is(err, ErrQuarantineFailed), errors.Is(err, ErrDetectionReporterUnavailable), errors.Is(err, ErrInvalidDetection):
		return ReasonQuarantineFailed
	default:
		return ReasonScanFailed
	}
}

func reasonForProgressError(err error) Reason {
	return reasonForWaitError(err, ReasonProgressFailed)
}
