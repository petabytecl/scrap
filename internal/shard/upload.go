package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/index"
)

const DefaultUploadConcurrency = 2

const maxOrphanedSeals = 64

const (
	uploadPollInterval        = 50 * time.Millisecond
	defaultUploadRetryBase    = time.Second
	defaultUploadAuthDelay    = time.Minute
	maxTransientUploadRetries = 5
	successesToRestoreUpload  = 5
	backoffMultiplier         = 2

	maxThrottleBackoff  = time.Minute
	maxTransientBackoff = 30 * time.Second
)

type UploadConfig struct {
	Enabled     bool
	Backend     backend.Backend
	CellID      string
	Concurrency int
	Pressure    UploadPressureConfig
	Metrics     UploadMetrics

	RetryBaseDelay time.Duration
	AuthRetryDelay time.Duration
}

type PendingUpload = index.PendingUpload

func (s *Shard) proposeSealBlock(ctx context.Context, upload index.PendingUpload) error {
	if !s.upload.Enabled {
		return nil
	}

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_SealBlock{
			SealBlock: &scrapv1.SealBlock{
				BlockId:         upload.BlockID,
				ShardId:         upload.ShardID,
				SealedSizeBytes: upload.SealedSizeBytes,
				SealedAtUs:      upload.SealedAtUs,
			},
		},
	}
	return s.proposeUploadCommand(ctx, "seal block", cmd)
}

func (s *Shard) proposeConfirmUpload(ctx context.Context, blockID uint64, backendKeyPrefix, etag string) error {
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConfirmUpload{
			ConfirmUpload: &scrapv1.ConfirmUpload{
				BlockId:          blockID,
				ShardId:          s.shardID,
				BackendKeyPrefix: backendKeyPrefix,
				ConfirmedAtUs:    time.Now().UnixMicro(),
				Etag:             etag,
			},
		},
	}
	return s.proposeUploadCommand(ctx, "confirm upload", cmd)
}

// proposeSeals proposes seal commands outside s.mu. Returns any that failed.
func (s *Shard) proposeSeals(ctx context.Context, seals []index.PendingUpload) []index.PendingUpload {
	var remaining []index.PendingUpload
	for _, seal := range seals {
		if err := s.proposeSealBlock(ctx, seal); err != nil {
			remaining = append(remaining, seal)
			s.logger.WarnContext(ctx, "shard: seal proposal failed, will retry", "block_id", seal.BlockID, "err", err)
		}
	}
	if len(remaining) > maxOrphanedSeals {
		s.logger.WarnContext(ctx, "shard: dropping oldest orphaned seals", "dropped", len(remaining)-maxOrphanedSeals)
		remaining = remaining[len(remaining)-maxOrphanedSeals:]
	}
	return remaining
}

func (s *Shard) proposeUploadCommand(ctx context.Context, op string, cmd *scrapv1.RaftCommand) error {
	// Stamp the deterministic block.upload trace context so the seal/confirm apply
	// spans land in the per-block upload trace (trace 2) on every voter (ADR 0013).
	if blockID, ok := uploadCommandBlockID(cmd); ok {
		bctx := trace.ContextWithSpanContext(ctx, blockTraceContext(s.uploadCellID(), s.shardID, blockID))
		injectTraceContext(bctx, cmd)
	}
	data, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("shard: marshal %s: %w", op, err)
	}
	if err := s.raft.Propose(ctx, data); err != nil {
		return fmt.Errorf("shard: propose %s: %w", op, err)
	}
	return nil
}

func (s *Shard) applySealBlock(seal *scrapv1.SealBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.idx.PutPendingUpload(index.PendingUpload{
		BlockID:         seal.GetBlockId(),
		ShardID:         seal.GetShardId(),
		SealedSizeBytes: seal.GetSealedSizeBytes(),
		SealedAtUs:      seal.GetSealedAtUs(),
	}); err != nil {
		return err
	}

	if s.blockWriter != nil && s.idxWriter != nil && s.blockWriter.BlockID() == seal.GetBlockId() {
		if err := s.idxWriter.Close(); err != nil {
			return fmt.Errorf("shard: close sealed block index: %w", err)
		}
		if err := s.blockWriter.Close(); err != nil {
			return fmt.Errorf("shard: close sealed block: %w", err)
		}
		if err := s.openNewBlock(); err != nil {
			return fmt.Errorf("shard: open block after seal apply: %w", err)
		}
	}

	if err := s.refreshUploadPressureLocked(); err != nil {
		return err
	}
	s.notifyUploadProcessor()
	return nil
}

func (s *Shard) applyConfirmUpload(confirm *scrapv1.ConfirmUpload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.idx.DeletePendingUpload(confirm.GetBlockId()); err != nil {
		return err
	}
	return s.refreshUploadPressureLocked()
}

func (s *Shard) AddOrphanedSealForTest(seal index.PendingUpload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orphanedSeals = append(s.orphanedSeals, seal)
}

func (s *Shard) RetryOrphanedSealsForTest(ctx context.Context) {
	s.mu.Lock()
	orphans := s.orphanedSeals
	s.orphanedSeals = nil
	s.mu.Unlock()

	remaining := s.proposeSeals(ctx, orphans)

	s.mu.Lock()
	s.orphanedSeals = remaining
	s.mu.Unlock()
}

func (s *Shard) PendingUploadsForTest() ([]PendingUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return collectPendingUploads(s.idx)
}

func (s *Shard) ConfirmUploadForTest(ctx context.Context, blockID uint64, backendKeyPrefix, etag string) error {
	return s.proposeConfirmUpload(ctx, blockID, backendKeyPrefix, etag)
}

func (s *Shard) startUploadProcessor() {
	if !s.upload.Enabled || s.upload.Backend == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.uploadCancel = cancel
	s.uploadDone = make(chan struct{})
	go s.runUploadProcessor(ctx)
}

func (s *Shard) runUploadProcessor(ctx context.Context) {
	defer close(s.uploadDone)

	ticker := time.NewTicker(uploadPollInterval)
	defer ticker.Stop()

	for {
		if s.IsLeader() {
			s.uploadPendingOnce(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-s.uploadNotify:
		case <-ticker.C:
		}
	}
}

func (s *Shard) uploadPendingOnce(ctx context.Context) {
	if s.uploadPaused() {
		return
	}

	uploads, err := s.pendingUploads()
	if err != nil {
		s.logger.ErrorContext(ctx, "upload: list pending uploads failed", "err", err)
		return
	}
	if len(uploads) == 0 {
		return
	}
	uploads = s.orderPendingUploads(uploads)

	jobs := make(chan PendingUpload)
	var wg sync.WaitGroup
	for range s.uploadConcurrency() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for upload := range jobs {
				if err := s.uploadAndConfirmWithRetry(ctx, upload); err != nil {
					s.markUploadRequeued(upload.BlockID)
					s.logger.ErrorContext(ctx, "upload: block upload failed", "err", err, "block_id", upload.BlockID)
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

func (s *Shard) pendingUploads() ([]PendingUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return collectPendingUploads(s.idx)
}

func collectPendingUploads(idx *index.Index) ([]PendingUpload, error) {
	iter, err := idx.PendingUploads()
	if err != nil {
		return nil, err
	}

	var uploads []PendingUpload
	for {
		upload, err := iter.Next()
		if err == nil {
			uploads = append(uploads, upload)
			continue
		}
		if errors.Is(err, io.EOF) {
			return uploads, nil
		}
		return nil, err
	}
}

func (s *Shard) uploadAndConfirmWithRetry(ctx context.Context, upload PendingUpload) error {
	start := time.Now()
	state := newUploadRetryState(s.uploadRetryBaseDelay())

	for {
		err := s.uploadAndConfirm(ctx, upload)
		if err == nil {
			s.recordUploadSuccess()
			s.recordUploadMetric("success", time.Since(start))
			return nil
		}

		retry, retryErr := s.handleUploadRetry(ctx, err, &state)
		if retryErr != nil {
			s.recordUploadMetric(uploadMetricStatus(retryErr), time.Since(start))
			return retryErr
		}
		if !retry {
			s.recordUploadMetric(uploadMetricStatus(err), time.Since(start))
			return err
		}
	}
}

type uploadRetryState struct {
	transientAttempts int
	corruptAttempts   int
	transientDelay    time.Duration
	throttleDelay     time.Duration
}

func newUploadRetryState(baseDelay time.Duration) uploadRetryState {
	return uploadRetryState{
		transientDelay: baseDelay,
		throttleDelay:  baseDelay,
	}
}

func (s *Shard) handleUploadRetry(ctx context.Context, err error, state *uploadRetryState) (bool, error) {
	switch backend.ErrorClass(err) {
	case backend.ClassThrottled:
		return s.handleThrottledUpload(ctx, state)
	case backend.ClassTransient:
		return handleTransientUpload(ctx, state)
	case backend.ClassAuth:
		s.pauseUploads(s.uploadAuthRetryDelay())
		return false, sleepUploadRetry(ctx, s.uploadAuthRetryDelay())
	case backend.ClassCorrupt:
		state.corruptAttempts++
		return state.corruptAttempts <= 1, nil
	default:
		return false, nil
	}
}

func (s *Shard) handleThrottledUpload(ctx context.Context, state *uploadRetryState) (bool, error) {
	s.recordUploadThrottle()
	if err := sleepUploadRetry(ctx, state.throttleDelay); err != nil {
		return false, err
	}
	state.throttleDelay = minDuration(state.throttleDelay*backoffMultiplier, maxThrottleBackoff)
	return true, nil
}

func handleTransientUpload(ctx context.Context, state *uploadRetryState) (bool, error) {
	state.transientAttempts++
	if state.transientAttempts > maxTransientUploadRetries {
		return false, nil
	}
	if err := sleepUploadRetry(ctx, state.transientDelay); err != nil {
		return false, err
	}
	state.transientDelay = minDuration(state.transientDelay*backoffMultiplier, maxTransientBackoff)
	return true, nil
}

func (s *Shard) uploadAndConfirm(ctx context.Context, upload PendingUpload) error {
	prefix := backendKeyPrefix(s.uploadCellID(), upload.ShardID, upload.BlockID)
	// Root backend I/O in the deterministic per-block trace so the PUT/verify spans
	// share a trace_id with the seal/confirm apply spans and the documents' links.
	bctx := trace.ContextWithSpanContext(ctx, blockTraceContext(s.uploadCellID(), s.shardID, upload.BlockID))
	blockAttr := trace.WithAttributes(blockIDAttribute(upload.BlockID))

	putBlkCtx, putBlk := s.writeTelemetry.StartSpan(bctx, "scrap.upload/put.blk", blockAttr)
	blk, err := s.uploadObject(putBlkCtx, upload.BlockID, prefix, "blk")
	putBlk.End(err)
	if err != nil {
		return err
	}
	putIdxCtx, putIdx := s.writeTelemetry.StartSpan(bctx, "scrap.upload/put.idx", blockAttr)
	idx, err := s.uploadObject(putIdxCtx, upload.BlockID, prefix, "idx")
	putIdx.End(err)
	if err != nil {
		return err
	}

	return s.proposeConfirmUpload(ctx, upload.BlockID, prefix, blk.ETag+","+idx.ETag)
}

func (s *Shard) uploadObject(ctx context.Context, blockID uint64, prefix, ext string) (backend.PutResult, error) {
	path := s.blockPath(blockID)
	if ext == "idx" {
		path = s.idxPath(blockID)
	}

	file, err := os.Open(path) //nolint:gosec // path is derived from controlled shard block IDs
	if err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: upload open %s: %w", backend.ErrPermanent, ext, err)
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: upload stat %s: %w", backend.ErrPermanent, ext, err)
	}

	key := prefix + "." + ext
	result, err := s.upload.Backend.PutObject(ctx, key, file, info.Size(), backend.PutOpts{})
	if err != nil {
		return backend.PutResult{}, err
	}

	meta, err := s.upload.Backend.HeadObject(ctx, key)
	if err != nil {
		s.recordUploadVerifyMetric("fail")
		return backend.PutResult{}, err
	}
	if meta.Size != result.Size || meta.ETag != result.ETag {
		s.recordUploadVerifyMetric("fail")
		return backend.PutResult{}, fmt.Errorf("%w: upload verification mismatch for %s", backend.ErrCorrupt, key)
	}
	s.recordUploadVerifyMetric("pass")
	return result, nil
}

func (s *Shard) uploadConcurrency() int {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if s.uploadCurrentConcurrency > 0 {
		return s.uploadCurrentConcurrency
	}
	return s.configuredUploadConcurrency()
}

func (s *Shard) configuredUploadConcurrency() int {
	if s.upload.Concurrency > 0 {
		return s.upload.Concurrency
	}
	return DefaultUploadConcurrency
}

func (s *Shard) resetUploadConcurrency() {
	s.uploadMu.Lock()
	s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	s.uploadSuccesses = 0
	concurrency := s.uploadCurrentConcurrency
	s.uploadMu.Unlock()

	s.setUploadConcurrencyMetric(concurrency)
}

func (s *Shard) recordUploadThrottle() {
	s.uploadMu.Lock()

	if s.uploadCurrentConcurrency == 0 {
		s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	}
	if s.uploadCurrentConcurrency > 1 {
		s.uploadCurrentConcurrency--
	}
	concurrency := s.uploadCurrentConcurrency
	s.uploadSuccesses = 0
	s.uploadMu.Unlock()

	s.setUploadConcurrencyMetric(concurrency)
}

func (s *Shard) recordUploadSuccess() {
	s.uploadMu.Lock()

	if s.uploadCurrentConcurrency == 0 {
		s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	}
	target := s.uploadConcurrencyTargetLocked()
	if s.uploadCurrentConcurrency >= target {
		s.uploadSuccesses = 0
		concurrency := s.uploadCurrentConcurrency
		s.uploadMu.Unlock()
		s.setUploadConcurrencyMetric(concurrency)
		return
	}

	s.uploadSuccesses++
	if s.uploadSuccesses >= successesToRestoreUpload {
		s.uploadCurrentConcurrency++
		s.uploadSuccesses = 0
	}
	concurrency := s.uploadCurrentConcurrency
	s.uploadMu.Unlock()

	s.setUploadConcurrencyMetric(concurrency)
}

func (s *Shard) markUploadRequeued(blockID uint64) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if s.uploadRequeued == nil {
		s.uploadRequeued = make(map[uint64]struct{})
	}
	s.uploadRequeued[blockID] = struct{}{}
}

func (s *Shard) orderPendingUploads(uploads []PendingUpload) []PendingUpload {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if len(s.uploadRequeued) == 0 {
		return uploads
	}

	ready := make([]PendingUpload, 0, len(uploads))
	requeued := make([]PendingUpload, 0, len(s.uploadRequeued))
	for _, upload := range uploads {
		if _, ok := s.uploadRequeued[upload.BlockID]; ok {
			requeued = append(requeued, upload)
			continue
		}
		ready = append(ready, upload)
	}
	s.uploadRequeued = make(map[uint64]struct{})
	return append(ready, requeued...)
}

func (s *Shard) pauseUploads(delay time.Duration) {
	s.uploadMu.Lock()
	s.uploadPausedUntil = time.Now().Add(delay)
	s.uploadAuthPaused = true
	s.uploadMu.Unlock()

	s.setUploadAuthPausedMetric(true)
}

func (s *Shard) uploadPaused() bool {
	s.uploadMu.Lock()
	paused := time.Now().Before(s.uploadPausedUntil)
	wasAuthPaused := s.uploadAuthPaused
	if !paused {
		s.uploadAuthPaused = false
	}
	s.uploadMu.Unlock()

	if !paused && wasAuthPaused {
		s.setUploadAuthPausedMetric(false)
	}
	return paused
}

func (s *Shard) uploadCellID() string {
	if s.upload.CellID != "" {
		return s.upload.CellID
	}
	return "local"
}

func (s *Shard) notifyUploadProcessor() {
	if s.uploadNotify == nil {
		return
	}
	select {
	case s.uploadNotify <- struct{}{}:
	default:
	}
}

func backendKeyPrefix(cellID string, shardID, blockID uint64) string {
	return filepath.ToSlash(fmt.Sprintf("%s/shards/%016x/%016x", cellID, shardID, blockID))
}

func (s *Shard) uploadRetryBaseDelay() time.Duration {
	if s.upload.RetryBaseDelay > 0 {
		return s.upload.RetryBaseDelay
	}
	return defaultUploadRetryBase
}

func (s *Shard) uploadAuthRetryDelay() time.Duration {
	if s.upload.AuthRetryDelay > 0 {
		return s.upload.AuthRetryDelay
	}
	return defaultUploadAuthDelay
}

func sleepUploadRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func uploadMetricStatus(err error) string {
	switch backend.ErrorClass(err) {
	case backend.ClassThrottled:
		return "throttled"
	case backend.ClassTransient:
		return "transient"
	case backend.ClassAuth:
		return "auth"
	case backend.ClassNotFound:
		return "not_found"
	case backend.ClassConflict:
		return "conflict"
	case backend.ClassCorrupt:
		return "corrupt"
	case backend.ClassPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}
