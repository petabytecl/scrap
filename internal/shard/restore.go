package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

const restoreCopyBufferSize = 1024 * 1024

type restoreStagingFile interface {
	io.Writer
	Sync() error
	Close() error
}

type blockRestoreCall struct {
	done chan struct{}
	err  error
}

func (s *Shard) ensureReadableBlockLockedForReason(ctx context.Context, blockID uint64, reason string) error {
	lifecycle, err := localblock.Classify(s.blocksDir, blockID)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: classify Block %d for read: %w", storeapi.ErrDataLoss, blockID, err)
	}

	switch lifecycle.State {
	case localblock.StateHot, localblock.StateHotCleanupNeeded:
		return nil
	case localblock.StateEvicted:
		s.mu.Unlock()
		if err := s.restoreEvictedBlockForReason(ctx, blockID, reason); err != nil {
			return err
		}
		s.mu.Lock()
		return nil
	case localblock.StateMetadataLoss, localblock.StateUnexpectedLoss:
		s.mu.Unlock()
		return fmt.Errorf("%w: Block %d local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	default:
		s.mu.Unlock()
		return fmt.Errorf("%w: Block %d unknown local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	}
}

func (s *Shard) restoreEvictedBlockForReason(ctx context.Context, blockID uint64, reason string) error {
	call, leader := s.beginRestore(blockID)
	if !leader {
		return waitRestore(ctx, call)
	}

	restoreCtx, cancel := detachedRestoreContext(ctx)
	defer cancel()
	call.err = s.restoreEvictedBlockOnce(restoreCtx, blockID, reason)
	close(call.done)

	s.restoreMu.Lock()
	delete(s.restores, blockID)
	s.restoreMu.Unlock()

	if call.err == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return call.err
}

func detachedRestoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	deadline, ok := ctx.Deadline()
	if !ok {
		return detached, func() {}
	}
	return context.WithDeadline(detached, deadline)
}

func (s *Shard) beginRestore(blockID uint64) (*blockRestoreCall, bool) {
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()

	if call, ok := s.restores[blockID]; ok {
		return call, false
	}
	if s.restores == nil {
		s.restores = make(map[uint64]*blockRestoreCall)
	}
	call := &blockRestoreCall{done: make(chan struct{})}
	s.restores[blockID] = call
	return call, true
}

func waitRestore(ctx context.Context, call *blockRestoreCall) error {
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Shard) restoreEvictedBlockOnce(ctx context.Context, blockID uint64, reason string) error {
	start := time.Now()
	err := s.restoreEvictedBlockOnceRecorded(ctx, blockID, reason)
	s.recordRestoreOutcome(blockID, reason, err, time.Since(start))
	return err
}

func (s *Shard) restoreEvictedBlockOnceRecorded(ctx context.Context, blockID uint64, reason string) error {
	input, err := s.restoreInput(ctx, blockID)
	if err != nil {
		return err
	}
	if input.lifecycle.State != localblock.StateEvicted {
		return nil
	}
	return s.downloadVerifyAndPublishRestore(ctx, input, reason)
}

func (s *Shard) RestoreBlockForRepair(ctx context.Context, blockID uint64) error {
	start := time.Now()
	err := s.restoreBlockForRepair(ctx, blockID)
	s.recordRestoreOutcome(blockID, localblock.RestoreReasonRepair, err, time.Since(start))
	return err
}

func (s *Shard) restoreBlockForRepair(ctx context.Context, blockID uint64) error {
	input, err := s.repairRestoreInput(ctx, blockID)
	if err != nil {
		return err
	}
	tmpBlockPath, err := s.downloadRestore(ctx, input)
	if err != nil {
		return err
	}
	published := false
	tmpPaths := []string{tmpBlockPath}
	defer func() {
		if !published {
			for _, tmpPath := range tmpPaths {
				_ = os.Remove(tmpPath)
			}
		}
	}()

	tmpIndexPath, err := s.downloadRestoreIndex(ctx, input)
	if err != nil {
		return err
	}
	tmpPaths = append(tmpPaths, tmpIndexPath)

	if err := verifyRestoredBlock(input, tmpBlockPath, tmpIndexPath); err != nil {
		return err
	}
	published, err = s.publishVerifiedRepairRestore(input, tmpBlockPath, tmpIndexPath)
	return err
}

func (s *Shard) recordRestoreOutcome(blockID uint64, reason string, err error, duration time.Duration) {
	result := "success"
	failureReason := eviction.RestoreFailureNone
	if err != nil {
		result = "failed"
		failureReason = restoreFailureReason(err)
	}
	s.recordCurrentRestoreFailure(blockID, failureReason)
	s.evictionMetricRecorder().RecordRestore(s.shardID, reason, result, failureReason, duration)
}

func (s *Shard) recordCurrentRestoreFailure(blockID uint64, failureReason string) {
	s.mu.Lock()
	if failureReason == eviction.RestoreFailureNone {
		delete(s.restoreFailuresByBlock, blockID)
	} else {
		if s.restoreFailuresByBlock == nil {
			s.restoreFailuresByBlock = make(map[uint64]string)
		}
		s.restoreFailuresByBlock[blockID] = failureReason
	}
	s.mu.Unlock()
}

func restoreFailureReason(err error) string {
	if err == nil {
		return eviction.RestoreFailureNone
	}
	if reason, ok := storeapi.UnavailableReason(err); ok {
		return reason
	}
	switch {
	case errors.Is(err, storeapi.ErrDataLoss):
		return eviction.RestoreFailureDataLoss
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return eviction.RestoreFailureCanceled
	default:
		return eviction.RestoreFailureUnknown
	}
}

type restoreInput struct {
	confirmed index.ConfirmedUpload
	lifecycle localblock.Lifecycle
	backend   backend.Backend
	blockPath string
	indexPath string
}

func (s *Shard) repairRestoreInput(ctx context.Context, blockID uint64) (restoreInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return restoreInput{}, err
	}
	confirmed, err := s.idx.GetConfirmedUpload(blockID)
	if err != nil {
		return restoreInput{}, fmt.Errorf("%w: Block %d has no committed ConfirmUpload for repair: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if s.upload.Backend == nil {
		return restoreInput{}, storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "Backend repair restore is not configured")
	}
	if err := requireQuarantinedRepairFiles(s.blocksDir, blockID); err != nil {
		return restoreInput{}, err
	}
	return restoreInput{
		confirmed: confirmed,
		backend:   s.upload.Backend,
		blockPath: s.blockPath(blockID),
		indexPath: s.idxPath(blockID) + block.QuarantineSuffix,
	}, nil
}

func (s *Shard) restoreInput(ctx context.Context, blockID uint64) (restoreInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return restoreInput{}, err
	}
	lifecycle, err := localblock.Classify(s.blocksDir, blockID)
	if err != nil {
		return restoreInput{}, fmt.Errorf("%w: classify Block %d for restore: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if lifecycle.State != localblock.StateEvicted {
		return restoreInput{lifecycle: lifecycle}, nil
	}
	confirmed, err := s.idx.GetConfirmedUpload(blockID)
	if err != nil {
		return restoreInput{}, fmt.Errorf("%w: Block %d has no committed ConfirmUpload: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if err := validateRestoreAuthority(confirmed, lifecycle); err != nil {
		return restoreInput{}, err
	}
	if s.upload.Backend == nil {
		return restoreInput{}, storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "Backend restore is not configured")
	}
	return restoreInput{
		confirmed: confirmed,
		lifecycle: lifecycle,
		backend:   s.upload.Backend,
		blockPath: s.blockPath(blockID),
		indexPath: s.idxPath(blockID),
	}, nil
}

func validateRestoreAuthority(confirmed index.ConfirmedUpload, lifecycle localblock.Lifecycle) error {
	marker := lifecycle.EvictionMarker
	if marker == nil {
		return fmt.Errorf("%w: evicted Block %d missing eviction marker", storeapi.ErrDataLoss, confirmed.BlockID)
	}
	switch {
	case marker.BackendKey != confirmed.BlockObject.Key:
		return fmt.Errorf("%w: eviction marker backend key mismatch for Block %d", storeapi.ErrDataLoss, confirmed.BlockID)
	case marker.SizeBytes != confirmed.BlockObject.SizeBytes:
		return fmt.Errorf("%w: eviction marker size mismatch for Block %d", storeapi.ErrDataLoss, confirmed.BlockID)
	case marker.ValidationToken != confirmed.BlockObject.ValidationToken:
		return fmt.Errorf("%w: eviction marker validation token mismatch for Block %d", storeapi.ErrDataLoss, confirmed.BlockID)
	default:
		return nil
	}
}

func (s *Shard) downloadVerifyAndPublishRestore(ctx context.Context, input restoreInput, reason string) error {
	tmpPath, err := s.downloadRestore(ctx, input)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := verifyRestoredBlock(input, tmpPath, input.indexPath); err != nil {
		return err
	}
	published, err = s.publishVerifiedRestore(input, tmpPath, reason)
	return err
}

func (s *Shard) publishVerifiedRestore(input restoreInput, tmpPath, reason string) (bool, error) {
	s.lifecycleMutationMu.Lock()
	defer s.lifecycleMutationMu.Unlock()

	if err := publishRestoredBlock(input, tmpPath); err != nil {
		return false, err
	}
	s.recordEvictionHealthBlockBestEffort(input.confirmed.BlockID)
	if err := s.recordSuccessfulRestore(input, reason); err != nil {
		return true, err
	}
	s.recordEvictionHealthBlockBestEffort(input.confirmed.BlockID)
	return true, nil
}

func (s *Shard) publishVerifiedRepairRestore(input restoreInput, tmpBlockPath, tmpIndexPath string) (bool, error) {
	s.lifecycleMutationMu.Lock()
	defer s.lifecycleMutationMu.Unlock()

	if err := publishRepairedBlock(input, tmpBlockPath, tmpIndexPath); err != nil {
		return false, err
	}
	s.recordEvictionHealthBlockBestEffort(input.confirmed.BlockID)
	if err := s.recordSuccessfulRestore(input, localblock.RestoreReasonRepair); err != nil {
		return true, err
	}
	s.recordEvictionHealthBlockBestEffort(input.confirmed.BlockID)
	return true, nil
}

func verifyRestoredBlock(input restoreInput, blkPath, idxPath string) error {
	if err := block.VerifyHeader(blkPath, input.confirmed.ShardID, input.confirmed.BlockID); err != nil {
		return fmt.Errorf("%w: restored Block %d header invalid: %w", storeapi.ErrDataLoss, input.confirmed.BlockID, err)
	}
	result, err := block.VerifyBlock(blkPath, idxPath)
	if err != nil {
		return fmt.Errorf("%w: restored Block %d verification failed: %w", storeapi.ErrDataLoss, input.confirmed.BlockID, err)
	}
	if len(result.CorruptFrames) > 0 {
		return fmt.Errorf("%w: restored Block %d has corrupt frames: %+v", storeapi.ErrDataLoss, input.confirmed.BlockID, result.CorruptFrames)
	}
	return nil
}

func publishRestoredBlock(input restoreInput, tmpPath string) error {
	if err := os.Rename(tmpPath, input.blockPath); err != nil {
		return fmt.Errorf("shard: publish restored Block %d: %w", input.confirmed.BlockID, err)
	}
	return syncDirectory(filepath.Dir(input.blockPath))
}

func requireQuarantinedRepairFiles(blocksDir string, blockID uint64) error {
	blkQ := block.FilePath(blocksDir, blockID) + block.QuarantineSuffix
	idxQ := block.IdxFilePath(blocksDir, blockID) + block.QuarantineSuffix
	if _, err := os.Stat(blkQ); err != nil {
		return fmt.Errorf("%w: Block %d quarantined data missing for repair: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if _, err := os.Stat(idxQ); err != nil {
		return fmt.Errorf("%w: Block %d quarantined index missing for repair: %w", storeapi.ErrDataLoss, blockID, err)
	}
	return nil
}

func publishRepairedBlock(input restoreInput, tmpBlockPath, tmpIndexPath string) error {
	blkQ := input.blockPath + block.QuarantineSuffix
	idxFinal := block.IdxFilePath(filepath.Dir(input.blockPath), input.confirmed.BlockID)
	idxQ := idxFinal + block.QuarantineSuffix
	rollback := repairPublishRollback{
		blockPath: input.blockPath,
		idxFinal:  idxFinal,
	}
	defer rollback.run()

	if err := os.Rename(tmpBlockPath, input.blockPath); err != nil {
		return fmt.Errorf("shard: publish repaired Block %d: %w", input.confirmed.BlockID, err)
	}
	rollback.blockPublished = true
	if err := os.Rename(tmpIndexPath, idxFinal); err != nil {
		return fmt.Errorf("shard: publish repaired index for Block %d: %w", input.confirmed.BlockID, err)
	}
	rollback.indexPublished = true
	if err := syncDirectory(filepath.Dir(input.blockPath)); err != nil {
		return err
	}
	rollback.committed = true
	return removeRepairQuarantineFiles(input.confirmed.BlockID, filepath.Dir(input.blockPath), blkQ, idxQ)
}

type repairPublishRollback struct {
	committed      bool
	blockPublished bool
	indexPublished bool
	blockPath      string
	idxFinal       string
}

func (r *repairPublishRollback) run() {
	if r.committed {
		return
	}
	if r.indexPublished {
		_ = os.Remove(r.idxFinal)
	}
	if r.blockPublished {
		_ = os.Remove(r.blockPath)
	}
}

func removeRepairQuarantineFiles(blockID uint64, dir, blkQ, idxQ string) error {
	if err := removeRepairQuarantineFile(blockID, "Block", blkQ); err != nil {
		return err
	}
	if err := removeRepairQuarantineFile(blockID, "index", idxQ); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func removeRepairQuarantineFile(blockID uint64, kind, path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("shard: remove quarantined %s %d after repair: %w", kind, blockID, err)
	}
	return nil
}

func (s *Shard) recordSuccessfulRestore(input restoreInput, reason string) error {
	if err := localblock.WriteRestoreMarker(s.blocksDir, localblock.RestoreMarker{
		BlockID:      input.confirmed.BlockID,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       localblock.RestoreSourceBackend,
		Reason:       reason,
	}); err != nil {
		return err
	}
	if err := os.Remove(localblock.EvictionMarkerPath(s.blocksDir, input.confirmed.BlockID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("shard: remove eviction marker after restore: %w", err)
	}
	return nil
}

func (s *Shard) downloadRestore(ctx context.Context, input restoreInput) (string, error) {
	return s.downloadRestoreObject(ctx, input, input.confirmed.BlockObject, "blk")
}

func (s *Shard) downloadRestoreIndex(ctx context.Context, input restoreInput) (string, error) {
	return s.downloadRestoreObject(ctx, input, input.confirmed.IndexObject, "idx")
}

func (s *Shard) downloadRestoreObject(ctx context.Context, input restoreInput, object index.BackendObjectMetadata, ext string) (string, error) {
	rc, meta, err := input.backend.GetObject(ctx, object.Key, backend.GetOpts{})
	if err != nil {
		return "", mapRestoreBackendError(err, input.confirmed.BlockID)
	}
	defer func() { _ = rc.Close() }()

	if err := validateRestoreObjectMeta(input.confirmed.BlockID, ext, object, meta); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(s.blocksDir, fmt.Sprintf(".%016x.%s.restore-*", input.confirmed.BlockID, ext))
	if err != nil {
		return "", fmt.Errorf("shard: create restore staging file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := copyRestoreObject(tmp, rc, input.confirmed.BlockID, ext, object); err != nil {
		return "", err
	}

	removeTemp = false
	return tmpPath, nil
}

func validateRestoreObjectMeta(blockID uint64, ext string, object index.BackendObjectMetadata, meta backend.ObjectMeta) error {
	if meta.Size != object.SizeBytes {
		return fmt.Errorf("%w: restored %s for Block %d size %d does not match confirmed size %d", storeapi.ErrDataLoss, ext, blockID, meta.Size, object.SizeBytes)
	}
	if meta.ETag != "" && meta.ETag != object.ValidationToken {
		return fmt.Errorf("%w: restored %s for Block %d validation token mismatch", storeapi.ErrDataLoss, ext, blockID)
	}
	return nil
}

func copyRestoreObject(tmp restoreStagingFile, rc io.Reader, blockID uint64, ext string, object index.BackendObjectMetadata) error {
	limit := object.SizeBytes
	if limit < math.MaxInt64 {
		limit++
	}
	written, err := copyRestoreReader(tmp, io.LimitReader(rc, limit), blockID)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if written != object.SizeBytes {
		_ = tmp.Close()
		return fmt.Errorf("%w: restored %s for Block %d copied %d bytes, expected %d", storeapi.ErrDataLoss, ext, blockID, written, object.SizeBytes)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("shard: sync restore staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("shard: close restore staging file: %w", err)
	}
	return nil
}

func copyRestoreReader(tmp restoreStagingFile, reader io.Reader, blockID uint64) (int64, error) {
	buf := make([]byte, restoreCopyBufferSize)
	var written int64
	for {
		n, readErr := reader.Read(buf)
		chunkWritten, writeErr := writeRestoreChunk(tmp, buf[:n], blockID)
		written += chunkWritten
		if writeErr != nil {
			return written, writeErr
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		return written, mapRestoreBackendError(readErr, blockID)
	}
}

func writeRestoreChunk(tmp restoreStagingFile, chunk []byte, blockID uint64) (int64, error) {
	if len(chunk) == 0 {
		return 0, nil
	}
	wrote, err := tmp.Write(chunk)
	if err != nil {
		return int64(wrote), fmt.Errorf("shard: write restore staging file for Block %d: %w", blockID, err)
	}
	if wrote != len(chunk) {
		return int64(wrote), fmt.Errorf("shard: write restore staging file for Block %d: %w", blockID, io.ErrShortWrite)
	}
	return int64(wrote), nil
}

func mapRestoreBackendError(err error, blockID uint64) error {
	switch backend.ErrorClass(err) {
	case backend.ClassThrottled, backend.ClassTransient, backend.ClassAuth:
		return storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, fmt.Sprintf("Backend restore unavailable for Block %d", blockID))
	case backend.ClassNotFound, backend.ClassCorrupt, backend.ClassPermanent, backend.ClassConflict:
		return fmt.Errorf("%w: Backend restore failed for Block %d: %w", storeapi.ErrDataLoss, blockID, err)
	default:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, fmt.Sprintf("Backend restore unavailable for Block %d", blockID))
	}
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // directory path is derived from Shard dataDir.
	if err != nil {
		return fmt.Errorf("shard: open directory for sync: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("shard: sync directory: %w", err)
	}
	return nil
}
