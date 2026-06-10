package shard

import (
	"context"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

type blockSealedEvent struct {
	BlockID         uint64
	ShardID         uint64
	SealedSizeBytes int64
	SealedAtUs      int64
}

func blockSealedEventFromPending(upload index.PendingUpload) blockSealedEvent {
	return blockSealedEvent{
		BlockID:         upload.BlockID,
		ShardID:         upload.ShardID,
		SealedSizeBytes: upload.SealedSizeBytes,
		SealedAtUs:      upload.SealedAtUs,
	}
}

func blockSealedEventFromCommand(seal *scrapv1.SealBlock) blockSealedEvent {
	return blockSealedEvent{
		BlockID:         seal.GetBlockId(),
		ShardID:         seal.GetShardId(),
		SealedSizeBytes: seal.GetSealedSizeBytes(),
		SealedAtUs:      seal.GetSealedAtUs(),
	}
}

func (e blockSealedEvent) pendingUpload() index.PendingUpload {
	return index.PendingUpload{
		BlockID:         e.BlockID,
		ShardID:         e.ShardID,
		SealedSizeBytes: e.SealedSizeBytes,
		SealedAtUs:      e.SealedAtUs,
	}
}

func (e blockSealedEvent) command() *scrapv1.SealBlock {
	return &scrapv1.SealBlock{
		BlockId:         e.BlockID,
		ShardId:         e.ShardID,
		SealedSizeBytes: e.SealedSizeBytes,
		SealedAtUs:      e.SealedAtUs,
	}
}

type uploadConfirmedEvent struct {
	BlockID          uint64
	ShardID          uint64
	ConfirmedAtUs    int64
	UploadGeneration int64
	BlockObject      index.BackendObjectMetadata
	IndexObject      index.BackendObjectMetadata
}

func uploadConfirmedEventFromCommand(confirm *scrapv1.ConfirmUpload) uploadConfirmedEvent {
	return uploadConfirmedEvent{
		BlockID:          confirm.GetBlockId(),
		ShardID:          confirm.GetShardId(),
		ConfirmedAtUs:    confirm.GetConfirmedAtUs(),
		UploadGeneration: confirm.GetUploadGeneration(),
		BlockObject:      indexBackendObject(confirm.GetBlockObject()),
		IndexObject:      indexBackendObject(confirm.GetIndexObject()),
	}
}

func uploadConfirmedEventFromUpload(upload index.ConfirmedUpload) uploadConfirmedEvent {
	return uploadConfirmedEvent{
		BlockID:          upload.BlockID,
		ShardID:          upload.ShardID,
		ConfirmedAtUs:    upload.ConfirmedAtUs,
		UploadGeneration: upload.UploadGeneration,
		BlockObject:      upload.BlockObject,
		IndexObject:      upload.IndexObject,
	}
}

func (e uploadConfirmedEvent) command() *scrapv1.ConfirmUpload {
	return &scrapv1.ConfirmUpload{
		BlockId:          e.BlockID,
		ShardId:          e.ShardID,
		BlockObject:      protoBackendObject(e.BlockObject),
		IndexObject:      protoBackendObject(e.IndexObject),
		ConfirmedAtUs:    e.ConfirmedAtUs,
		UploadGeneration: e.UploadGeneration,
	}
}

type uploadOutbox struct {
	blocksDir string
	idx       *index.Index
	lifecycle *blockUploadLifecycle
}

func newUploadOutbox(blocksDir string, idx *index.Index, lifecycle *blockUploadLifecycle) uploadOutbox {
	return uploadOutbox{
		blocksDir: blocksDir,
		idx:       idx,
		lifecycle: lifecycle,
	}
}

func (s *Shard) uploadOutboxLocked() uploadOutbox {
	return newUploadOutbox(s.blocksDir, s.idx, s.blockUploadLifecycleLocked())
}

func (o uploadOutbox) RecordBlockSealed(event blockSealedEvent) {
	o.lifecycle.recordLocalSeal(event.pendingUpload())
}

func (o uploadOutbox) ApplyBlockSealed(event blockSealedEvent) error {
	return o.lifecycle.applyCommittedSeal(o.idx, event.command())
}

func (o uploadOutbox) ApplyUploadConfirmed(event uploadConfirmedEvent) (index.ConfirmedUpload, error) {
	return o.lifecycle.applyCommittedConfirm(o.blocksDir, o.idx, event.command())
}

func (o uploadOutbox) ConfirmedUploadAuthority(blockID uint64) (index.ConfirmedUpload, bool, error) {
	return o.lifecycle.confirmedUploadAuthority(o.blocksDir, blockID)
}

func (o uploadOutbox) RefreshPressure(uploads *uploadController) error {
	return o.lifecycle.refreshPressure(o.idx, uploads)
}

func (o uploadOutbox) BeginBlockSealRetry(now, retryAfter time.Time) []index.PendingUpload {
	return o.lifecycle.beginSealRetry(now, retryAfter)
}

func (o uploadOutbox) MarkBlockSealRetryFailed(blockID uint64, retryAfter time.Time) {
	o.lifecycle.markSealRetryFailed(blockID, retryAfter)
}

func (o uploadOutbox) ForgetUploadObligation(blockID uint64) {
	o.lifecycle.forgetUploadObligation(blockID)
}

func (o uploadOutbox) UploadObligationCount() int {
	return o.lifecycle.obligationCount()
}

func proposeUploadConfirmedEvent(ctx context.Context, prop raftProposer, cellID string, shardID uint64, event uploadConfirmedEvent) error {
	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConfirmUpload{
			ConfirmUpload: event.command(),
		},
	}
	return proposeUploadCommand(ctx, prop, cellID, shardID, "confirm upload", cmd)
}
