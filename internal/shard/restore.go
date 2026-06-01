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
	"github.com/petabytecl/scrap/internal/index"
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

func (s *Shard) ensureReadableBlockLocked(ctx context.Context, blockID uint64) error {
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: classify Block %d for read: %w", storeapi.ErrDataLoss, blockID, err)
	}

	switch lifecycle.State {
	case LocalBlockStateHot, LocalBlockStateHotCleanupNeeded:
		return nil
	case LocalBlockStateEvicted:
		s.mu.Unlock()
		if err := s.restoreEvictedBlock(ctx, blockID); err != nil {
			return err
		}
		s.mu.Lock()
		return nil
	case LocalBlockStateMetadataLoss, LocalBlockStateUnexpectedLoss:
		s.mu.Unlock()
		return fmt.Errorf("%w: Block %d local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	default:
		s.mu.Unlock()
		return fmt.Errorf("%w: Block %d unknown local state %s", storeapi.ErrDataLoss, blockID, lifecycle.State)
	}
}

func (s *Shard) restoreEvictedBlock(ctx context.Context, blockID uint64) error {
	return s.restoreEvictedBlockForReason(ctx, blockID, RestoreReasonRead)
}

func (s *Shard) restoreEvictedBlockForReason(ctx context.Context, blockID uint64, reason string) error {
	call, leader := s.beginRestore(blockID)
	if !leader {
		return waitRestore(ctx, call)
	}

	call.err = s.restoreEvictedBlockOnce(context.WithoutCancel(ctx), blockID, reason)
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
	input, err := s.restoreInput(ctx, blockID)
	if err != nil {
		return err
	}
	if input.lifecycle.State != LocalBlockStateEvicted {
		return nil
	}
	return s.downloadVerifyAndPublishRestore(ctx, input, reason)
}

type restoreInput struct {
	confirmed index.ConfirmedUpload
	lifecycle LocalBlockLifecycle
	backend   backend.Backend
	blockPath string
	indexPath string
}

func (s *Shard) restoreInput(ctx context.Context, blockID uint64) (restoreInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return restoreInput{}, err
	}
	lifecycle, err := ClassifyLocalBlock(s.blocksDir, blockID)
	if err != nil {
		return restoreInput{}, fmt.Errorf("%w: classify Block %d for restore: %w", storeapi.ErrDataLoss, blockID, err)
	}
	if lifecycle.State != LocalBlockStateEvicted {
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

func validateRestoreAuthority(confirmed index.ConfirmedUpload, lifecycle LocalBlockLifecycle) error {
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

	if err := verifyRestoredBlock(input, tmpPath); err != nil {
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
	if err := s.recordSuccessfulRestore(input, reason); err != nil {
		return true, err
	}
	return true, nil
}

func verifyRestoredBlock(input restoreInput, tmpPath string) error {
	if err := block.VerifyHeader(tmpPath, input.confirmed.ShardID, input.confirmed.BlockID); err != nil {
		return fmt.Errorf("%w: restored Block %d header invalid: %w", storeapi.ErrDataLoss, input.confirmed.BlockID, err)
	}
	result, err := block.VerifyBlock(tmpPath, input.indexPath)
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

func (s *Shard) recordSuccessfulRestore(input restoreInput, reason string) error {
	if err := WriteRestoreMarker(s.blocksDir, RestoreMarker{
		BlockID:      input.confirmed.BlockID,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       RestoreSourceBackend,
		Reason:       reason,
	}); err != nil {
		return err
	}
	if err := os.Remove(EvictionMarkerPath(s.blocksDir, input.confirmed.BlockID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("shard: remove eviction marker after restore: %w", err)
	}
	return nil
}

func (s *Shard) downloadRestore(ctx context.Context, input restoreInput) (string, error) {
	rc, meta, err := input.backend.GetObject(ctx, input.confirmed.BlockObject.Key, backend.GetOpts{})
	if err != nil {
		return "", mapRestoreBackendError(err, input.confirmed.BlockID)
	}
	defer func() { _ = rc.Close() }()

	if err := validateRestoreObjectMeta(input.confirmed, meta); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(s.blocksDir, fmt.Sprintf(".%016x.blk.restore-*", input.confirmed.BlockID))
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

	if err := copyRestoreObject(tmp, rc, input.confirmed); err != nil {
		return "", err
	}

	removeTemp = false
	return tmpPath, nil
}

func validateRestoreObjectMeta(confirmed index.ConfirmedUpload, meta backend.ObjectMeta) error {
	if meta.Size != confirmed.BlockObject.SizeBytes {
		return fmt.Errorf("%w: restored Block %d size %d does not match confirmed size %d", storeapi.ErrDataLoss, confirmed.BlockID, meta.Size, confirmed.BlockObject.SizeBytes)
	}
	if meta.ETag != "" && meta.ETag != confirmed.BlockObject.ValidationToken {
		return fmt.Errorf("%w: restored Block %d validation token mismatch", storeapi.ErrDataLoss, confirmed.BlockID)
	}
	return nil
}

func copyRestoreObject(tmp restoreStagingFile, rc io.Reader, confirmed index.ConfirmedUpload) error {
	limit := confirmed.BlockObject.SizeBytes
	if limit < math.MaxInt64 {
		limit++
	}
	written, err := copyRestoreReader(tmp, io.LimitReader(rc, limit), confirmed.BlockID)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if written != confirmed.BlockObject.SizeBytes {
		_ = tmp.Close()
		return fmt.Errorf("%w: restored Block %d copied %d bytes, expected %d", storeapi.ErrDataLoss, confirmed.BlockID, written, confirmed.BlockObject.SizeBytes)
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
