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
	// Collect orphans under s.mu, then release to avoid holding s.mu during Raft I/O.
	orphans := s.orphanedSeals
	s.orphanedSeals = nil

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
	orphans = append(orphans, seal)

	s.mu.Unlock()
	remaining := s.proposeSeals(ctx, orphans)
	s.mu.Lock()

	s.orphanedSeals = remaining
	return nil
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
		if !strings.HasSuffix(name, ".blk") {
			continue
		}
		hexPart := strings.TrimSuffix(name, ".blk")
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			return 0, fmt.Errorf("shard: malformed block filename: %s", name)
		}
		if id > maxID {
			maxID = id
		}
	}
	return maxID + 1, nil
}
