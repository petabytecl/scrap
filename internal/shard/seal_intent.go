package shard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
)

// sealIntent is a fsynced local record that a Block was closed and must enter
// the Upload Outbox (H-07 / ADR 0037). It bridges the window between closing
// the Block files and committing SealBlock via Raft.
type sealIntent struct {
	Version         int    `json:"version"`
	BlockID         uint64 `json:"block_id"`
	ShardID         uint64 `json:"shard_id"`
	SealedSizeBytes int64  `json:"sealed_size_bytes"`
	SealedAtUs      int64  `json:"sealed_at_us"`
}

const sealIntentVersion = 1

func sealIntentPath(blocksDir string, blockID uint64) string {
	return filepath.Join(blocksDir, fmt.Sprintf("%016x.seal-intent.json", blockID))
}

func (s *Shard) writeSealIntentLocked(blockID uint64, sealedSize int64) error {
	intent := sealIntent{
		Version:         sealIntentVersion,
		BlockID:         blockID,
		ShardID:         s.shardID,
		SealedSizeBytes: sealedSize,
		SealedAtUs:      time.Now().UnixMicro(),
	}
	if err := localblock.WriteJSONMarker(sealIntentPath(s.blocksDir, blockID), intent); err != nil {
		return fmt.Errorf("shard: write seal intent for Block %d: %w", blockID, err)
	}
	return nil
}

func readSealIntent(blocksDir string, blockID uint64) (sealIntent, error) {
	var intent sealIntent
	if err := localblock.ReadJSONMarker(sealIntentPath(blocksDir, blockID), &intent); err != nil {
		return sealIntent{}, err
	}
	if intent.Version != sealIntentVersion {
		return sealIntent{}, fmt.Errorf("shard: seal intent version %d", intent.Version)
	}
	if intent.BlockID != blockID {
		return sealIntent{}, fmt.Errorf("shard: seal intent block_id mismatch: key %d value %d", blockID, intent.BlockID)
	}
	return intent, nil
}

func removeSealIntent(blocksDir string, blockID uint64) error {
	path := sealIntentPath(blocksDir, blockID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("shard: remove seal intent for Block %d: %w", blockID, err)
	}
	return nil
}

func (s *Shard) sealIntentPendingUpload(intent sealIntent) index.PendingUpload {
	return index.PendingUpload{
		BlockID:         intent.BlockID,
		ShardID:         intent.ShardID,
		SealedSizeBytes: intent.SealedSizeBytes,
		SealedAtUs:      intent.SealedAtUs,
	}
}

// reconcileClosedBlocksIntoUploadOutboxLocked records local upload obligations
// for every closed Block that still needs SealBlock / Backend upload (H-07).
// Callers must hold s.mu.
func (s *Shard) reconcileClosedBlocksIntoUploadOutboxLocked() error { //nolint:cyclop // startup/leadership reconciliation must inspect every closed Block and seal intent
	if !s.upload.Enabled {
		return nil
	}
	openID := uint64(0)
	if s.blockWriter != nil {
		openID = s.blockWriter.BlockID()
	}
	entries, err := os.ReadDir(s.blocksDir)
	if err != nil {
		return fmt.Errorf("shard: read blocks dir for seal reconcile: %w", err)
	}
	seen := make(map[uint64]struct{})
	for _, entry := range entries {
		name := entry.Name()
		id, ok, parseErr := blockIDFromLocalLifecycleName(name)
		if parseErr != nil || !ok {
			continue
		}
		if _, already := seen[id]; already {
			continue
		}
		seen[id] = struct{}{}
		if openID != 0 && id >= openID {
			continue
		}
		if err := s.reconcileClosedBlockLocked(id); err != nil {
			return err
		}
	}
	return s.refreshUploadPressureLocked()
}

func (s *Shard) reconcileClosedBlockLocked(blockID uint64) error { //nolint:cyclop // per-Block reconcile branches across confirmed/pending/seal-intent/local-file states
	if _, err := s.idx.GetConfirmedUpload(blockID); err == nil {
		_ = removeSealIntent(s.blocksDir, blockID)
		return nil
	} else if !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		return fmt.Errorf("shard: reconcile confirmed upload %d: %w", blockID, err)
	}
	if _, err := s.idx.GetPendingUpload(blockID); err == nil {
		return nil
	} else if !errors.Is(err, index.ErrPendingUploadNotFound) {
		return fmt.Errorf("shard: reconcile pending upload %d: %w", blockID, err)
	}

	intent, intentErr := readSealIntent(s.blocksDir, blockID)
	if intentErr == nil {
		s.uploadOutboxLocked().RecordBlockSealed(blockSealedEventFromPending(s.sealIntentPendingUpload(intent)))
		return nil
	}
	if !os.IsNotExist(intentErr) {
		if _, statErr := os.Stat(sealIntentPath(s.blocksDir, blockID)); !os.IsNotExist(statErr) {
			return fmt.Errorf("shard: read seal intent %d: %w", blockID, intentErr)
		}
	}

	info, err := os.Stat(s.blockPath(blockID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("shard: stat closed Block %d: %w", blockID, err)
	}
	if _, err := os.Stat(s.idxPath(blockID)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("shard: stat closed Block %d index: %w", blockID, err)
	}
	seal := index.PendingUpload{
		BlockID:         blockID,
		ShardID:         s.shardID,
		SealedSizeBytes: info.Size(),
		SealedAtUs:      info.ModTime().UnixMicro(),
	}
	s.uploadOutboxLocked().RecordBlockSealed(blockSealedEventFromPending(seal))
	return nil
}
