package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"
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

var errConfirmUploadSealedMetadataMismatch = errors.New("shard: confirm upload sealed metadata mismatch")

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

// raftProposer is the consensus write seam shared by the seal path (on the core)
// and the upload controller (for confirm-upload commands).
type raftProposer interface {
	Propose(ctx context.Context, data []byte) error
}

// Propose is the Shard's consensus write facade. It satisfies raftProposer and
// the uploadCore seam used by the upload controller.
func (s *Shard) Propose(ctx context.Context, data []byte) error {
	return s.raft.Propose(ctx, data)
}

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
	return proposeUploadCommand(ctx, s.raft, cellIDOrLocal(s.upload.CellID), s.shardID, "seal block", cmd)
}

// proposeSeals proposes seal commands outside s.mu. Local upload obligations are
// removed only by applySealBlock, after Raft has committed the Upload Outbox row.
func (s *Shard) proposeSeals(ctx context.Context, seals []index.PendingUpload) {
	for _, seal := range seals {
		if err := s.proposeSealBlock(ctx, seal); err != nil {
			s.mu.Lock()
			s.blockUploadLifecycleLocked().markSealRetryFailed(seal.BlockID, time.Now().Add(s.uploadSealRetryDelay()))
			s.mu.Unlock()
			s.logger.WarnContext(ctx, "shard: seal proposal failed, will retry", "block_id", seal.BlockID, "err", err)
		}
	}
}

func (s *Shard) uploadSealRetryDelay() time.Duration {
	if s.upload.RetryBaseDelay > 0 {
		return s.upload.RetryBaseDelay
	}
	return defaultUploadRetryBase
}

// proposeUploadCommand stamps the deterministic block.upload trace context so the
// seal/confirm apply spans land in the per-block upload trace (trace 2) on every
// voter (ADR 0013), then marshals and proposes the command.
func proposeUploadCommand(ctx context.Context, prop raftProposer, cellID string, shardID uint64, op string, cmd *scrapv1.RaftCommand) error {
	if blockID, ok := uploadCommandBlockID(cmd); ok {
		bctx := trace.ContextWithSpanContext(ctx, blockTraceContext(cellID, shardID, blockID))
		injectTraceContext(bctx, cmd)
	}
	data, err := proto.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("shard: marshal %s: %w", op, err)
	}
	if err := prop.Propose(ctx, data); err != nil {
		return fmt.Errorf("shard: propose %s: %w", op, err)
	}
	return nil
}

func (s *Shard) applySealBlock(seal *scrapv1.SealBlock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.blockUploadLifecycleLocked().applyCommittedSeal(s.idx, seal); err != nil {
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
	s.uploads.Notify()
	return nil
}

func (s *Shard) applyConfirmUpload(confirm *scrapv1.ConfirmUpload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	confirmed, err := s.blockUploadLifecycleLocked().applyCommittedConfirm(s.blocksDir, s.idx, confirm)
	if err != nil {
		if errors.Is(err, index.ErrPendingUploadNotFound) || errors.Is(err, errConfirmUploadSealedMetadataMismatch) {
			if s.logger != nil {
				s.logger.Warn("shard: confirm upload skipped unsafe catalog", "block_id", confirm.GetBlockId(), "err", err)
			}
			if closeErr := s.closeConfirmedOpenBlockLocked(confirm.GetBlockId()); closeErr != nil {
				return closeErr
			}
			if deleteErr := s.idx.DeletePendingUpload(confirm.GetBlockId()); deleteErr != nil {
				return fmt.Errorf("shard: clear unsafe pending upload %d: %w", confirm.GetBlockId(), deleteErr)
			}
			s.blockUploadLifecycleLocked().forgetUploadObligation(confirm.GetBlockId())
			s.recordEvictionHealthMetadataLossBlock(confirm.GetBlockId())
			return s.refreshUploadPressureLocked()
		}
		return err
	}
	if err := s.closeConfirmedOpenBlockLocked(confirm.GetBlockId()); err != nil {
		return err
	}
	s.recordEvictionHealthBlockBestEffort(confirmed.BlockID)
	return s.refreshUploadPressureLocked()
}

func (s *Shard) closeConfirmedOpenBlockLocked(blockID uint64) error {
	if s.blockWriter == nil || s.blockWriter.BlockID() != blockID {
		return nil
	}
	if s.idxWriter == nil {
		return fmt.Errorf("shard: confirmed block %d index writer is not open", blockID)
	}
	if err := s.idxWriter.Close(); err != nil {
		return fmt.Errorf("shard: close confirmed block index: %w", err)
	}
	if err := s.blockWriter.Close(); err != nil {
		return fmt.Errorf("shard: close confirmed block: %w", err)
	}
	if err := s.openNewBlock(); err != nil {
		return fmt.Errorf("shard: open block after confirm upload apply: %w", err)
	}
	return nil
}

func confirmedUploadFromCommand(confirm *scrapv1.ConfirmUpload, sealedSize int64) index.ConfirmedUpload {
	return index.ConfirmedUpload{
		BlockID:         confirm.GetBlockId(),
		ShardID:         confirm.GetShardId(),
		ConfirmedAtUs:   confirm.GetConfirmedAtUs(),
		SealedSizeBytes: sealedSize,
		BlockObject:     indexBackendObject(confirm.GetBlockObject()),
		IndexObject:     indexBackendObject(confirm.GetIndexObject()),
	}
}

func validateConfirmedUploadMatchesSeal(upload index.ConfirmedUpload) error {
	if upload.BlockObject.SizeBytes != upload.SealedSizeBytes {
		return fmt.Errorf(
			"%w: confirmed block object size %d does not match sealed size %d for block %d",
			errConfirmUploadSealedMetadataMismatch,
			upload.BlockObject.SizeBytes,
			upload.SealedSizeBytes,
			upload.BlockID,
		)
	}
	return nil
}

func indexBackendObject(meta *scrapv1.BackendObjectMetadata) index.BackendObjectMetadata {
	if meta == nil {
		return index.BackendObjectMetadata{}
	}
	return index.BackendObjectMetadata{
		Key:             meta.GetKey(),
		SizeBytes:       meta.GetSizeBytes(),
		ValidationToken: meta.GetValidationToken(),
	}
}

func (s *Shard) AddOrphanedSealForTest(seal index.PendingUpload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockUploadLifecycleLocked().recordLocalSeal(seal)
	if err := s.refreshUploadPressureLocked(); err != nil {
		panic(err)
	}
}

func (s *Shard) retryUploadObligations(ctx context.Context) {
	s.mu.Lock()
	pendingRetry := s.beginUploadObligationRetryLocked(time.Now())
	s.mu.Unlock()

	s.proposeSeals(ctx, pendingRetry)
}

func (s *Shard) beginUploadObligationRetryLocked(now time.Time) []index.PendingUpload {
	return s.blockUploadLifecycleLocked().beginSealRetry(now, now.Add(s.uploadSealRetryDelay()))
}

func (s *Shard) RetryOrphanedSealsForTest(ctx context.Context) {
	s.retryUploadObligations(ctx)
}

func (s *Shard) PendingUploadsForTest() ([]PendingUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return collectPendingUploads(s.idx)
}

func (s *Shard) ConfirmUploadForTest(ctx context.Context, upload index.ConfirmedUpload) error {
	return s.uploads.proposeConfirmUpload(ctx, upload)
}

func (s *Shard) ConfirmedUploadForTest(blockID uint64) (index.ConfirmedUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.idx.GetConfirmedUpload(blockID)
}

// pendingUploads is the projection-read seam: it reads the pending-upload outbox
// under the core's mutex on behalf of the upload controller.
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

func backendKeyPrefix(cellID string, shardID, blockID uint64) string {
	return filepath.ToSlash(fmt.Sprintf("%s/shards/%016x/%016x", cellID, shardID, blockID))
}

func cellIDOrLocal(cellID string) string {
	if cellID != "" {
		return cellID
	}
	return "local"
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
