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

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/index"
)

const DefaultUploadConcurrency = 2

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

func (s *Shard) proposeUploadCommand(ctx context.Context, op string, cmd *scrapv1.RaftCommand) error {
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
	s.notifyUploadProcessor()
	return nil
}

func (s *Shard) applyConfirmUpload(confirm *scrapv1.ConfirmUpload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.idx.DeletePendingUpload(confirm.GetBlockId())
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
	s.resetUploadConcurrency()

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
	state := newUploadRetryState(s.uploadRetryBaseDelay())

	for {
		err := s.uploadAndConfirm(ctx, upload)
		if err == nil {
			s.recordUploadSuccess()
			return nil
		}

		retry, retryErr := s.handleUploadRetry(ctx, err, &state)
		if retryErr != nil {
			return retryErr
		}
		if !retry {
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

	blk, err := s.uploadObject(ctx, upload.BlockID, prefix, "blk")
	if err != nil {
		return err
	}
	idx, err := s.uploadObject(ctx, upload.BlockID, prefix, "idx")
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
		return backend.PutResult{}, err
	}
	if meta.Size != result.Size || meta.ETag != result.ETag {
		return backend.PutResult{}, fmt.Errorf("%w: upload verification mismatch for %s", backend.ErrCorrupt, key)
	}
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
	defer s.uploadMu.Unlock()

	s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	s.uploadSuccesses = 0
}

func (s *Shard) recordUploadThrottle() {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if s.uploadCurrentConcurrency == 0 {
		s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	}
	if s.uploadCurrentConcurrency > 1 {
		s.uploadCurrentConcurrency--
	}
	s.uploadSuccesses = 0
}

func (s *Shard) recordUploadSuccess() {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if s.uploadCurrentConcurrency == 0 {
		s.uploadCurrentConcurrency = s.configuredUploadConcurrency()
	}
	if s.uploadCurrentConcurrency >= s.configuredUploadConcurrency() {
		s.uploadSuccesses = 0
		return
	}

	s.uploadSuccesses++
	if s.uploadSuccesses >= successesToRestoreUpload {
		s.uploadCurrentConcurrency++
		s.uploadSuccesses = 0
	}
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
	defer s.uploadMu.Unlock()

	s.uploadPausedUntil = time.Now().Add(delay)
}

func (s *Shard) uploadPaused() bool {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	return time.Now().Before(s.uploadPausedUntil)
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
