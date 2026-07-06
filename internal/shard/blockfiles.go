package shard

// Block file lifecycle: open, seal, rotate, close, and discover local Block and
// Frame index files for a Shard.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func (s *Shard) sealAndOpenNew(ctx context.Context) error {
	blockID := s.blockWriter.BlockID()
	sealedSize := s.blockWriter.Offset()
	if err := s.idxWriter.Close(); err != nil {
		return err
	}
	if err := s.blockWriter.Close(); err != nil {
		return err
	}
	if err := s.openNewBlock(); err != nil {
		return err
	}

	seal := index.PendingUpload{
		BlockID:         blockID,
		ShardID:         s.shardID,
		SealedSizeBytes: sealedSize,
		SealedAtUs:      time.Now().UnixMicro(),
	}
	if s.upload.Enabled {
		s.uploadOutboxLocked().RecordBlockSealed(blockSealedEventFromPending(seal))
		if err := s.refreshUploadPressureLocked(); err != nil {
			return err
		}
	}
	admissionErr := s.uploads.rejectWrite()
	pendingRetry := s.beginUploadObligationRetryLocked(time.Now())

	s.mu.Unlock()
	s.proposeSeals(ctx, pendingRetry)
	s.scanner.Notify()
	s.mu.Lock()

	return admissionErr
}

func (s *Shard) openNewBlock() error {
	id := s.nextBlockID
	s.nextBlockID++

	bw, err := block.NewWriter(s.blockPath(id), s.shardID, id)
	if err != nil {
		return err
	}
	iw, err := block.NewIndexWriter(s.idxPath(id))
	if err != nil {
		_ = bw.Close() // best-effort cleanup on index writer failure
		return err
	}
	// Persist the new .blk/.idx directory entries. Without this a power loss
	// after a committed write can lose the freshly created Block files while the
	// raft log durably references the Block ID, leaving replay unable to open
	// the index and the payload bytes (not in the raft log) gone.
	if err := syncDir(s.blocksDir); err != nil {
		_ = iw.Close()
		_ = bw.Close()
		return fmt.Errorf("shard: sync blocks dir after open block %d: %w", id, err)
	}
	s.blockWriter = bw
	s.idxWriter = iw
	return nil
}

func (s *Shard) closeBlockAndIdx() {
	if s.idxWriter != nil {
		_ = s.idxWriter.Close() // best-effort cleanup
	}
	if s.blockWriter != nil {
		_ = s.blockWriter.Close() // best-effort cleanup
	}
}

func (s *Shard) blockPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.blk", id))
}

func (s *Shard) idxPath(id uint64) string {
	return filepath.Join(s.blocksDir, fmt.Sprintf("%016x.idx", id))
}

// safeUint64ToInt64 converts a uint64 to int64, clamping to math.MaxInt64 on overflow.
func safeUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// sweepOrphanedStagingFiles removes restore and replica-repair staging files
// left behind by a crash. They can each hold a full Block of bytes, the
// lifecycle scan deliberately ignores them, and nothing else ever reclaims
// them. Runs before the Shard serves, so no staging file can be live.
func sweepOrphanedStagingFiles(blocksDir string) error {
	patterns := []string{".*.restore-*", "*" + replicaRepairSuffix, "*" + replicaRepairSuffix + ".tmp"}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(blocksDir, pattern))
		if err != nil {
			return fmt.Errorf("shard: glob staging files: %w", err)
		}
		for _, match := range matches {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("shard: remove orphaned staging file: %w", err)
			}
		}
	}
	return nil
}

func scanMaxBlockID(blocksDir string) (uint64, error) {
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("shard: read blocks dir: %w", err)
	}

	var maxID uint64
	for _, e := range entries {
		name := e.Name()
		id, ok, err := blockIDFromLocalLifecycleName(name)
		if err != nil {
			return 0, fmt.Errorf("shard: malformed block filename %s: %w", name, err)
		}
		if !ok {
			continue
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID + 1, nil
}

func blockIDFromLocalLifecycleName(name string) (uint64, bool, error) {
	// Quarantined Block files must keep reserving their ID: Block IDs are
	// never reused, and a fully quarantined highest Block would otherwise be
	// reallocated at the next startup.
	for _, suffix := range []string{".blk", ".idx", ".blk.eviction.json", ".blk.restore.json", ".blk.quarantine", ".idx.quarantine", ".confirmed-upload.json"} {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(name, suffix), 16, 64)
		if err != nil {
			return 0, false, err
		}
		return id, true, nil
	}
	return 0, false, nil
}
