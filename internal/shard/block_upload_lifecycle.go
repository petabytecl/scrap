package shard

import (
	"errors"
	"fmt"
	"os"
	"time"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

type blockUploadLifecycle struct {
	obligations             uploadObligations
	committedConfirmUploads map[uint64]index.ConfirmedUpload
}

func newBlockUploadLifecycle() *blockUploadLifecycle {
	return &blockUploadLifecycle{
		committedConfirmUploads: make(map[uint64]index.ConfirmedUpload),
	}
}

func (s *Shard) blockUploadLifecycleLocked() *blockUploadLifecycle {
	if s.blockUploads == nil {
		s.blockUploads = newBlockUploadLifecycle()
	}
	return s.blockUploads
}

func (l *blockUploadLifecycle) recordLocalSeal(upload index.PendingUpload) {
	l.obligations.recordLocal(upload)
}

func (l *blockUploadLifecycle) markSealRetryFailed(blockID uint64, retryAfter time.Time) {
	l.obligations.markRetryFailed(blockID, retryAfter)
}

func (l *blockUploadLifecycle) beginSealRetry(now, retryAfter time.Time) []index.PendingUpload {
	return l.obligations.beginRetry(now, retryAfter)
}

func (l *blockUploadLifecycle) obligationCount() int {
	return l.obligations.len()
}

func (l *blockUploadLifecycle) forgetUploadObligation(blockID uint64) {
	l.obligations.forget(blockID)
}

func (l *blockUploadLifecycle) applyCommittedSeal(idx *index.Index, seal *scrapv1.SealBlock) error {
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         seal.GetBlockId(),
		ShardID:         seal.GetShardId(),
		SealedSizeBytes: seal.GetSealedSizeBytes(),
		SealedAtUs:      seal.GetSealedAtUs(),
	}); err != nil {
		return err
	}
	l.obligations.forget(seal.GetBlockId())
	return nil
}

func (l *blockUploadLifecycle) applyCommittedConfirm(blocksDir string, idx *index.Index, confirm *scrapv1.ConfirmUpload) (index.ConfirmedUpload, error) {
	pending, err := idx.GetPendingUpload(confirm.GetBlockId())
	if err != nil {
		return l.applyConfirmWithoutPending(blocksDir, idx, confirm, err)
	}
	if pending.UploadGeneration != confirm.GetUploadGeneration() {
		return index.ConfirmedUpload{}, fmt.Errorf(
			"%w: block %d pending generation %d confirm generation %d",
			errConfirmUploadGenerationMismatch,
			confirm.GetBlockId(),
			pending.UploadGeneration,
			confirm.GetUploadGeneration(),
		)
	}

	confirmed, err := l.putConfirmedUploadFromCommand(blocksDir, idx, confirm, pending.SealedSizeBytes)
	if err != nil {
		return index.ConfirmedUpload{}, err
	}
	if err := idx.DeletePendingUpload(confirm.GetBlockId()); err != nil {
		return index.ConfirmedUpload{}, err
	}
	l.recordCommittedConfirmUpload(confirmed)
	l.obligations.forget(confirm.GetBlockId())
	return confirmed, nil
}

func (l *blockUploadLifecycle) applyConfirmWithoutPending(
	blocksDir string,
	idx *index.Index,
	confirm *scrapv1.ConfirmUpload,
	pendingErr error,
) (index.ConfirmedUpload, error) {
	if !errors.Is(pendingErr, index.ErrPendingUploadNotFound) {
		return index.ConfirmedUpload{}, fmt.Errorf("shard: confirm upload missing sealed metadata for block %d: %w", confirm.GetBlockId(), pendingErr)
	}

	existing, catalogErr := idx.GetConfirmedUpload(confirm.GetBlockId())
	if catalogErr != nil {
		if !errors.Is(catalogErr, index.ErrConfirmedUploadNotFound) {
			return index.ConfirmedUpload{}, fmt.Errorf("shard: get confirmed upload for block %d: %w", confirm.GetBlockId(), catalogErr)
		}
		return index.ConfirmedUpload{}, fmt.Errorf("shard: confirm upload missing sealed metadata for block %d: %w", confirm.GetBlockId(), pendingErr)
	}
	if existing.UploadGeneration != confirm.GetUploadGeneration() {
		return index.ConfirmedUpload{}, fmt.Errorf(
			"%w: block %d confirmed generation %d confirm generation %d",
			errConfirmUploadGenerationMismatch,
			confirm.GetBlockId(),
			existing.UploadGeneration,
			confirm.GetUploadGeneration(),
		)
	}

	confirmed, err := l.putConfirmedUploadFromCommand(blocksDir, idx, confirm, existing.SealedSizeBytes)
	if err != nil {
		return index.ConfirmedUpload{}, err
	}
	l.recordCommittedConfirmUpload(confirmed)
	return confirmed, nil
}

func (l *blockUploadLifecycle) refreshPressure(idx *index.Index, uploads *uploadController) error {
	pending, err := collectPendingUploads(idx)
	if err != nil {
		return err
	}
	uploads.SetPressure(l.obligations.pressureStats(pending))
	return nil
}

func (l *blockUploadLifecycle) confirmedUploadAuthority(blocksDir string, blockID uint64) (index.ConfirmedUpload, bool, error) {
	confirmed, ok := l.committedConfirmUploads[blockID]
	if ok {
		return confirmed, false, nil
	}
	confirmed, err := l.readCommittedConfirmUploadAuthority(blocksDir, blockID)
	if err != nil {
		return index.ConfirmedUpload{}, false, err
	}
	return confirmed, true, nil
}

func (l *blockUploadLifecycle) readCommittedConfirmUploadAuthority(blocksDir string, blockID uint64) (index.ConfirmedUpload, error) {
	if blocksDir == "" {
		return index.ConfirmedUpload{}, index.ErrConfirmedUploadNotFound
	}
	confirmed, err := readConfirmedUploadAuthority(blocksDir, blockID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return index.ConfirmedUpload{}, index.ErrConfirmedUploadNotFound
		}
		return index.ConfirmedUpload{}, err
	}
	l.recordCommittedConfirmUpload(confirmed)
	return confirmed, nil
}

func (l *blockUploadLifecycle) putConfirmedUploadFromCommand(
	blocksDir string,
	idx *index.Index,
	confirm *scrapv1.ConfirmUpload,
	sealedSize int64,
) (index.ConfirmedUpload, error) {
	confirmed := confirmedUploadFromCommand(confirm, sealedSize)
	if err := validateConfirmedUploadMatchesSeal(confirmed); err != nil {
		return index.ConfirmedUpload{}, err
	}
	if err := writeConfirmedUploadAuthority(blocksDir, confirmed); err != nil {
		return index.ConfirmedUpload{}, err
	}
	if err := idx.PutConfirmedUpload(confirmed); err != nil {
		return index.ConfirmedUpload{}, err
	}
	return confirmed, nil
}

func (l *blockUploadLifecycle) recordCommittedConfirmUpload(confirmed index.ConfirmedUpload) {
	if l.committedConfirmUploads == nil {
		l.committedConfirmUploads = make(map[uint64]index.ConfirmedUpload)
	}
	l.committedConfirmUploads[confirmed.BlockID] = confirmed
}
