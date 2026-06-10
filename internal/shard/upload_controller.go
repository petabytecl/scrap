package shard

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

// uploadCore is the narrow seam the upload controller uses to reach the Shard's
// projection-authority core: consensus (propose/leadership), the pending-upload
// projection read (under the core's mutex), and block/index file paths. The
// controller never touches s.idx or s.mu directly.
type uploadCore interface {
	Propose(ctx context.Context, data []byte) error
	IsLeader() bool
	retryUploadObligations(ctx context.Context)
	pendingUploads() ([]PendingUpload, error)
	blockPath(blockID uint64) string
	idxPath(blockID uint64) string
}

// uploadController owns the backend-upload responsibility extracted from Shard:
// the upload processor goroutine, adaptive concurrency, retry/pause state, and
// the upload-pressure cache. It owns its own mutex; the Shard core pushes
// pressure to it (SetPressure) and wakes it (Notify) but never reads its state.
type uploadController struct {
	core      uploadCore
	cfg       UploadConfig
	shardID   uint64
	logger    *slog.Logger
	telemetry WriteStageRecorder
	scrubGate *pressurePauseGate // shared: controller pauses, deep scrubber waits

	cancel context.CancelFunc
	done   chan struct{}
	notify chan struct{}

	mu                 sync.Mutex
	currentConcurrency int
	successes          int
	requeued           map[uint64]struct{}
	pausedUntil        time.Time
	authPaused         bool
	pressureLevel      UploadPressureLevel
	pendingBytes       int64
	pendingBlocks      int
}

func newUploadController(core uploadCore, cfg UploadConfig, shardID uint64, logger *slog.Logger, telemetry WriteStageRecorder, scrubGate *pressurePauseGate) *uploadController {
	return &uploadController{
		core:      core,
		cfg:       cfg,
		shardID:   shardID,
		logger:    logger,
		telemetry: telemetry,
		scrubGate: scrubGate,
		notify:    make(chan struct{}, 1),
	}
}

// Start launches the upload processor goroutine when uploads are enabled. It is
// a no-op when uploads are disabled.
func (c *uploadController) Start() {
	if !c.cfg.Enabled {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.runProcessor(ctx)
}

// Stop cancels the upload processor and waits for it to finish. It deliberately
// cancels-and-joins only: the notify channel is never closed, so an in-flight
// apply on the core can call Notify after Stop without panicking (collaborators
// stop before Raft; see the #331 design constraint).
func (c *uploadController) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
	}
}

// Notify wakes the upload processor. Safe to call after Stop (non-blocking send
// to a buffered, never-closed channel).
func (c *uploadController) Notify() {
	if c.notify == nil {
		return
	}
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *uploadController) runProcessor(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(uploadPollInterval)
	defer ticker.Stop()

	for {
		if c.core.IsLeader() {
			c.core.retryUploadObligations(ctx)
			c.processPendingOnce(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-c.notify:
		case <-ticker.C:
		}
	}
}

func (c *uploadController) processPendingOnce(ctx context.Context) {
	if c.uploadProcessingPaused() {
		return
	}

	uploads, err := c.core.pendingUploads()
	if err != nil {
		c.logger.ErrorContext(ctx, "upload: list pending uploads failed", "err", err)
		return
	}
	if len(uploads) == 0 {
		return
	}
	uploads = c.orderPending(uploads)

	jobs := make(chan PendingUpload)
	var wg sync.WaitGroup
	for range c.concurrency() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for upload := range jobs {
				if err := c.uploadAndConfirmWithRetry(ctx, upload); err != nil {
					c.markRequeued(upload.BlockID)
					c.logger.ErrorContext(ctx, "upload: block upload failed", "err", err, "block_id", upload.BlockID)
				}
			}
		}()
	}

	for _, upload := range uploads {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- upload:
		}
	}
	close(jobs)
	wg.Wait()
}

func (c *uploadController) uploadProcessingPaused() bool {
	return c.cfg.Backend == nil || c.paused()
}

func (c *uploadController) uploadAndConfirmWithRetry(ctx context.Context, upload PendingUpload) error {
	start := time.Now()
	state := newUploadRetryState(c.retryBaseDelay())

	for {
		err := c.uploadAndConfirm(ctx, upload)
		if err == nil {
			c.recordSuccess()
			c.recordUploadMetric("success", time.Since(start))
			return nil
		}

		retry, retryErr := c.handleRetry(ctx, err, &state)
		if retryErr != nil {
			c.recordUploadMetric(uploadMetricStatus(retryErr), time.Since(start))
			return retryErr
		}
		if !retry {
			c.recordUploadMetric(uploadMetricStatus(err), time.Since(start))
			return err
		}
	}
}

func (c *uploadController) handleRetry(ctx context.Context, err error, state *uploadRetryState) (bool, error) {
	switch backend.ErrorClass(err) {
	case backend.ClassThrottled:
		return c.handleThrottled(ctx, state)
	case backend.ClassTransient:
		return handleTransientUpload(ctx, state)
	case backend.ClassNotFound:
		return handleTransientUpload(ctx, state)
	case backend.ClassAuth:
		c.pause(c.authRetryDelay())
		return false, sleepUploadRetry(ctx, c.authRetryDelay())
	case backend.ClassCorrupt:
		state.corruptAttempts++
		return state.corruptAttempts <= 1, nil
	default:
		return false, nil
	}
}

func (c *uploadController) handleThrottled(ctx context.Context, state *uploadRetryState) (bool, error) {
	c.recordThrottle()
	if err := sleepUploadRetry(ctx, state.throttleDelay); err != nil {
		return false, err
	}
	state.throttleDelay = minDuration(state.throttleDelay*backoffMultiplier, maxThrottleBackoff)
	return true, nil
}

func (c *uploadController) uploadAndConfirm(ctx context.Context, upload PendingUpload) error {
	prefix := backendKeyPrefix(c.cellID(), upload.ShardID, upload.BlockID, upload.UploadGeneration)
	// Root backend I/O in the deterministic per-block trace so the PUT/verify spans
	// share a trace_id with the seal/confirm apply spans and the documents' links.
	bctx := trace.ContextWithSpanContext(ctx, blockTraceContext(c.cellID(), c.shardID, upload.BlockID))
	blockAttr := trace.WithAttributes(blockIDAttribute(upload.BlockID))

	putBlkCtx, putBlk := c.telemetry.StartSpan(bctx, "scrap.upload/put.blk", blockAttr)
	blk, err := c.uploadObject(putBlkCtx, upload.BlockID, prefix, "blk")
	putBlk.End(err)
	if err != nil {
		return err
	}
	putIdxCtx, putIdx := c.telemetry.StartSpan(bctx, "scrap.upload/put.idx", blockAttr)
	idx, err := c.uploadObject(putIdxCtx, upload.BlockID, prefix, "idx")
	putIdx.End(err)
	if err != nil {
		return err
	}

	return c.proposeConfirmUpload(ctx, index.ConfirmedUpload{
		BlockID:          upload.BlockID,
		ShardID:          upload.ShardID,
		ConfirmedAtUs:    time.Now().UnixMicro(),
		SealedSizeBytes:  upload.SealedSizeBytes,
		UploadGeneration: upload.UploadGeneration,
		BlockObject:      blk,
		IndexObject:      idx,
	})
}

func (c *uploadController) uploadObject(ctx context.Context, blockID uint64, prefix, ext string) (index.BackendObjectMetadata, error) {
	path := c.core.blockPath(blockID)
	if ext == "idx" {
		path = c.core.idxPath(blockID)
	}

	file, err := os.Open(path) //nolint:gosec // path is derived from controlled shard block IDs
	if err != nil {
		return index.BackendObjectMetadata{}, fmt.Errorf("%w: upload open %s: %w", backend.ErrPermanent, ext, err)
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return index.BackendObjectMetadata{}, fmt.Errorf("%w: upload stat %s: %w", backend.ErrPermanent, ext, err)
	}

	key := prefix + "." + ext
	result, err := c.cfg.Backend.PutObject(ctx, key, file, info.Size(), backend.PutOpts{})
	if err != nil {
		return index.BackendObjectMetadata{}, err
	}

	meta, err := c.cfg.Backend.HeadObject(ctx, key)
	if err != nil {
		c.recordVerifyMetric("fail")
		return index.BackendObjectMetadata{}, err
	}
	if meta.Size != result.Size || meta.ETag != result.ETag {
		c.recordVerifyMetric("fail")
		return index.BackendObjectMetadata{}, fmt.Errorf("%w: upload verification mismatch for %s", backend.ErrCorrupt, key)
	}
	if result.ETag == "" {
		c.recordVerifyMetric("fail")
		return index.BackendObjectMetadata{}, fmt.Errorf("%w: upload verification missing validation token for %s", backend.ErrCorrupt, key)
	}
	c.recordVerifyMetric("pass")
	return index.BackendObjectMetadata{
		Key:             key,
		SizeBytes:       result.Size,
		ValidationToken: result.ETag,
	}, nil
}

func (c *uploadController) proposeConfirmUpload(ctx context.Context, upload index.ConfirmedUpload) error {
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConfirmUpload{
			ConfirmUpload: &scrapv1.ConfirmUpload{
				BlockId:          upload.BlockID,
				ShardId:          upload.ShardID,
				BlockObject:      protoBackendObject(upload.BlockObject),
				IndexObject:      protoBackendObject(upload.IndexObject),
				ConfirmedAtUs:    upload.ConfirmedAtUs,
				UploadGeneration: upload.UploadGeneration,
			},
		},
	}
	return proposeUploadCommand(ctx, c.core, c.cellID(), c.shardID, "confirm upload", cmd)
}

func protoBackendObject(meta index.BackendObjectMetadata) *scrapv1.BackendObjectMetadata {
	return &scrapv1.BackendObjectMetadata{
		Key:             meta.Key,
		SizeBytes:       meta.SizeBytes,
		ValidationToken: meta.ValidationToken,
	}
}

func (c *uploadController) concurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.currentConcurrency > 0 {
		return c.currentConcurrency
	}
	return c.configuredConcurrency()
}

func (c *uploadController) configuredConcurrency() int {
	if c.cfg.Concurrency > 0 {
		return c.cfg.Concurrency
	}
	return DefaultUploadConcurrency
}

func (c *uploadController) resetConcurrency() {
	c.mu.Lock()
	c.currentConcurrency = c.configuredConcurrency()
	c.successes = 0
	concurrency := c.currentConcurrency
	c.mu.Unlock()

	c.setConcurrencyMetric(concurrency)
}

func (c *uploadController) recordThrottle() {
	c.mu.Lock()

	if c.currentConcurrency == 0 {
		c.currentConcurrency = c.configuredConcurrency()
	}
	if c.currentConcurrency > 1 {
		c.currentConcurrency--
	}
	concurrency := c.currentConcurrency
	c.successes = 0
	c.mu.Unlock()

	c.setConcurrencyMetric(concurrency)
}

func (c *uploadController) recordSuccess() {
	c.mu.Lock()

	if c.currentConcurrency == 0 {
		c.currentConcurrency = c.configuredConcurrency()
	}
	target := c.concurrencyTargetLocked()
	if c.currentConcurrency >= target {
		c.successes = 0
		concurrency := c.currentConcurrency
		c.mu.Unlock()
		c.setConcurrencyMetric(concurrency)
		return
	}

	c.successes++
	if c.successes >= successesToRestoreUpload {
		c.currentConcurrency++
		c.successes = 0
	}
	concurrency := c.currentConcurrency
	c.mu.Unlock()

	c.setConcurrencyMetric(concurrency)
}

func (c *uploadController) markRequeued(blockID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.requeued == nil {
		c.requeued = make(map[uint64]struct{})
	}
	c.requeued[blockID] = struct{}{}
}

func (c *uploadController) orderPending(uploads []PendingUpload) []PendingUpload {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.requeued) == 0 {
		return uploads
	}

	ready := make([]PendingUpload, 0, len(uploads))
	requeued := make([]PendingUpload, 0, len(c.requeued))
	for _, upload := range uploads {
		if _, ok := c.requeued[upload.BlockID]; ok {
			requeued = append(requeued, upload)
			continue
		}
		ready = append(ready, upload)
	}
	c.requeued = make(map[uint64]struct{})
	return append(ready, requeued...)
}

func (c *uploadController) pause(delay time.Duration) {
	c.mu.Lock()
	c.pausedUntil = time.Now().Add(delay)
	c.authPaused = true
	c.mu.Unlock()

	c.setAuthPausedMetric(true)
}

func (c *uploadController) paused() bool {
	c.mu.Lock()
	paused := time.Now().Before(c.pausedUntil)
	wasAuthPaused := c.authPaused
	if !paused {
		c.authPaused = false
	}
	c.mu.Unlock()

	if !paused && wasAuthPaused {
		c.setAuthPausedMetric(false)
	}
	return paused
}

func (c *uploadController) cellID() string {
	return cellIDOrLocal(c.cfg.CellID)
}

func (c *uploadController) retryBaseDelay() time.Duration {
	if c.cfg.RetryBaseDelay > 0 {
		return c.cfg.RetryBaseDelay
	}
	return defaultUploadRetryBase
}

func (c *uploadController) authRetryDelay() time.Duration {
	if c.cfg.AuthRetryDelay > 0 {
		return c.cfg.AuthRetryDelay
	}
	return defaultUploadAuthDelay
}

// SetPressure recomputes the upload-pressure level and adaptive concurrency from
// stats pushed by the core's apply loop, toggles the shared scrub pause gate on
// Critical, and records pressure metrics. This is the "pressure push" seam.
func (c *uploadController) SetPressure(stats uploadPressureStats) {
	cfg := normalizeUploadPressureConfig(c.cfg.Pressure)
	level := cfg.levelFor(stats.pendingBytes)

	c.mu.Lock()
	previousLevel := c.pressureLevel
	c.pressureLevel = level
	c.pendingBytes = stats.pendingBytes
	c.pendingBlocks = stats.pendingBlocks
	concurrency := c.applyPressureConcurrencyLocked(previousLevel, level)
	c.mu.Unlock()

	if c.scrubGate != nil {
		c.scrubGate.SetPaused(level == UploadPressureLevelCritical)
	}
	c.recordPressureMetrics(stats, level, concurrency)
}

func (c *uploadController) applyPressureConcurrencyLocked(previousLevel, level UploadPressureLevel) int {
	configured := c.configuredConcurrency()
	if c.currentConcurrency == 0 {
		c.currentConcurrency = configured
	}

	switch {
	case level >= UploadPressureLevelWarn:
		target := c.concurrencyTargetLocked()
		if c.currentConcurrency < target {
			c.currentConcurrency = target
			c.successes = 0
		}
	case previousLevel >= UploadPressureLevelWarn && c.currentConcurrency > configured:
		c.currentConcurrency = configured
		c.successes = 0
	}
	return c.currentConcurrency
}

func (c *uploadController) concurrencyTargetLocked() int {
	configured := c.configuredConcurrency()
	if c.pressureLevel >= UploadPressureLevelWarn {
		return configured * uploadPressureConcurrencyMultiplier
	}
	return configured
}

func (c *uploadController) snapshot() UploadPressureSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	return UploadPressureSnapshot{
		Level:         c.pressureLevel,
		PendingBytes:  c.pendingBytes,
		PendingBlocks: c.pendingBlocks,
	}
}

func (c *uploadController) rejectWrite() error {
	c.mu.Lock()
	level := c.pressureLevel
	c.mu.Unlock()

	if level < UploadPressureLevelPressure {
		return nil
	}
	return storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonUploadPressure, "upload pressure")
}

func (c *uploadController) recordPressureMetrics(stats uploadPressureStats, level UploadPressureLevel, concurrency int) {
	if c.cfg.Metrics == nil {
		return
	}
	c.cfg.Metrics.SetPending(c.shardID, stats.pendingBytes, stats.pendingBlocks)
	c.cfg.Metrics.SetPressureLevel(c.shardID, level)
	c.cfg.Metrics.SetConcurrency(c.shardID, concurrency)
}

func (c *uploadController) recordUploadMetric(status string, duration time.Duration) {
	if c.cfg.Metrics != nil {
		c.cfg.Metrics.RecordUpload(c.shardID, status, duration)
	}
}

func (c *uploadController) recordVerifyMetric(status string) {
	if c.cfg.Metrics != nil {
		c.cfg.Metrics.RecordVerify(c.shardID, status)
	}
}

func (c *uploadController) setConcurrencyMetric(concurrency int) {
	if c.cfg.Metrics != nil {
		c.cfg.Metrics.SetConcurrency(c.shardID, concurrency)
	}
}

func (c *uploadController) setAuthPausedMetric(paused bool) {
	if c.cfg.Metrics != nil {
		c.cfg.Metrics.SetAuthPaused(c.shardID, paused)
	}
}
