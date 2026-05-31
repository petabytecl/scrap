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
			s.uploadObligations.markRetryFailed(seal.BlockID)
			s.mu.Unlock()
			s.logger.WarnContext(ctx, "shard: seal proposal failed, will retry", "block_id", seal.BlockID, "err", err)
		}
	}
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

	if err := s.idx.PutPendingUpload(index.PendingUpload{
		BlockID:         seal.GetBlockId(),
		ShardID:         seal.GetShardId(),
		SealedSizeBytes: seal.GetSealedSizeBytes(),
		SealedAtUs:      seal.GetSealedAtUs(),
	}); err != nil {
		return err
	}
	s.uploadObligations.forget(seal.GetBlockId())

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

	if err := s.idx.DeletePendingUpload(confirm.GetBlockId()); err != nil {
		return err
	}
	s.uploadObligations.forget(confirm.GetBlockId())
	return s.refreshUploadPressureLocked()
}

func (s *Shard) AddOrphanedSealForTest(seal index.PendingUpload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploadObligations.recordLocal(seal)
	if err := s.refreshUploadPressureLocked(); err != nil {
		panic(err)
	}
}

func (s *Shard) retryUploadObligations(ctx context.Context) {
	s.mu.Lock()
	pendingRetry := s.uploadObligations.beginRetry()
	s.mu.Unlock()

	s.proposeSeals(ctx, pendingRetry)
}

func (s *Shard) RetryOrphanedSealsForTest(ctx context.Context) {
	s.retryUploadObligations(ctx)
}

func (s *Shard) PendingUploadsForTest() ([]PendingUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return collectPendingUploads(s.idx)
}

func (s *Shard) ConfirmUploadForTest(ctx context.Context, blockID uint64, backendKeyPrefix, etag string) error {
	return s.uploads.proposeConfirmUpload(ctx, blockID, backendKeyPrefix, etag)
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
