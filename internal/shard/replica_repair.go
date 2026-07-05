package shard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/block"
)

const replicaRepairSuffix = ".replica-repair"

type replicaRepairPaths struct {
	blkFinal  string
	idxFinal  string
	blkStaged string
	idxStaged string
}

func (s *Shard) repairReplicaOffsetLocked(ctx context.Context, blockID uint64, wantOffset int64) error {
	if s.blockTransferer == nil {
		return errors.New("no Block transferer configured")
	}
	if wantOffset < block.HeaderSize {
		return fmt.Errorf("repair offset %d before Block header", wantOffset)
	}

	addrs := s.replicaRepairAddrsLocked()
	if len(addrs) == 0 {
		return errors.New("no replica repair peers configured")
	}

	var lastErr error
	for _, addr := range addrs {
		if err := s.repairReplicaBlockFromPeerLocked(ctx, addr, blockID, wantOffset); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("all replica repair peers failed: %w", lastErr)
}

func (s *Shard) replicaRepairAddrsLocked() []string {
	seen := make(map[string]struct{}, len(s.peers))
	addrs := make([]string, 0, len(s.peers))

	leaderID := s.raft.LeaderID()
	if leaderID != 0 && leaderID != s.raftID {
		if addr, ok := s.peers[leaderID]; ok {
			addrs = appendUniqueReplicaRepairAddr(addrs, seen, addr)
		}
	}
	for _, addr := range s.replicationAddrs() {
		addrs = appendUniqueReplicaRepairAddr(addrs, seen, addr)
	}
	return addrs
}

func appendUniqueReplicaRepairAddr(addrs []string, seen map[string]struct{}, addr string) []string {
	if addr == "" {
		return addrs
	}
	if _, ok := seen[addr]; ok {
		return addrs
	}
	seen[addr] = struct{}{}
	return append(addrs, addr)
}

func (s *Shard) repairReplicaBlockFromPeerLocked(ctx context.Context, addr string, blockID uint64, wantOffset int64) error {
	blkData, idxData, err := s.blockTransferer.TransferBlock(ctx, addr, s.shardID, blockID)
	if err != nil {
		return fmt.Errorf("fetch replacement Block from %s: %w", addr, err)
	}

	paths := s.replicaRepairPaths(blockID)
	if err := cleanupReplicaRepairStaging(paths); err != nil {
		return err
	}
	if err := writeSyncedReplicaRepairFile(paths.blkStaged, blkData); err != nil {
		return fmt.Errorf("write replacement Block: %w", err)
	}
	if err := writeSyncedReplicaRepairFile(paths.idxStaged, idxData); err != nil {
		_ = cleanupReplicaRepairStaging(paths)
		return fmt.Errorf("write replacement index: %w", err)
	}
	defer func() { _ = cleanupReplicaRepairStaging(paths) }()

	if err := prepareReplicaRepairStaging(paths, s.shardID, blockID, wantOffset); err != nil {
		return err
	}
	return s.promoteReplicaRepairLocked(paths, blockID, wantOffset)
}

func (s *Shard) replicaRepairPaths(blockID uint64) replicaRepairPaths {
	blkPath := s.blockPath(blockID)
	idxPath := s.idxPath(blockID)
	return replicaRepairPaths{
		blkFinal:  blkPath,
		idxFinal:  idxPath,
		blkStaged: blkPath + replicaRepairSuffix,
		idxStaged: idxPath + replicaRepairSuffix,
	}
}

func prepareReplicaRepairStaging(paths replicaRepairPaths, shardID, blockID uint64, wantOffset int64) error {
	info, err := os.Stat(paths.blkStaged)
	if err != nil {
		return fmt.Errorf("stat replacement Block: %w", err)
	}
	if info.Size() < wantOffset {
		return fmt.Errorf("replacement Block size %d before repair offset %d", info.Size(), wantOffset)
	}
	if err := validateReplicaRepairIndexTail(paths.idxStaged, wantOffset); err != nil {
		return err
	}
	if info.Size() > wantOffset {
		if err := os.Truncate(paths.blkStaged, wantOffset); err != nil {
			return fmt.Errorf("truncate replacement Block to repair offset: %w", err)
		}
		if err := syncReplicaRepairFile(paths.blkStaged); err != nil {
			return fmt.Errorf("sync truncated replacement Block: %w", err)
		}
	}
	if err := block.VerifyHeader(paths.blkStaged, shardID, blockID); err != nil {
		return fmt.Errorf("verify replacement Block header: %w", err)
	}
	result, err := block.VerifyBlock(paths.blkStaged, paths.idxStaged)
	if err != nil {
		return fmt.Errorf("verify replacement Block: %w", err)
	}
	if len(result.CorruptFrames) > 0 {
		return fmt.Errorf("verify replacement Block: %d corrupt Frames", len(result.CorruptFrames))
	}
	return nil
}

func validateReplicaRepairIndexTail(idxPath string, wantOffset int64) error {
	ir, err := block.OpenIndexReader(idxPath)
	if err != nil {
		return fmt.Errorf("open replacement index: %w", err)
	}
	defer func() { _ = ir.Close() }()

	for _, entry := range ir.Entries() {
		if entry.FirstFrameOff >= wantOffset {
			return fmt.Errorf("replacement index has Document at offset %d after repair offset %d", entry.FirstFrameOff, wantOffset)
		}
	}
	return nil
}

func (s *Shard) promoteReplicaRepairLocked(paths replicaRepairPaths, blockID uint64, wantOffset int64) (err error) {
	if err := s.idxWriter.Close(); err != nil {
		return fmt.Errorf("close replica index before repair: %w", err)
	}
	if err := s.blockWriter.Close(); err != nil {
		return fmt.Errorf("close replica Block before repair: %w", err)
	}
	// Both live writers are closed from here on. Any failure below must
	// reinstall writers over whatever now sits at the final paths, or the
	// Shard keeps closed handles and every subsequent append fails.
	defer func() {
		if err != nil {
			s.reopenReplicaWritersBestEffort(paths, blockID)
		}
	}()

	if err := os.Rename(paths.blkStaged, paths.blkFinal); err != nil {
		return fmt.Errorf("promote replacement Block: %w", err)
	}
	if err := os.Rename(paths.idxStaged, paths.idxFinal); err != nil {
		return fmt.Errorf("promote replacement index: %w", err)
	}
	if err := syncReplicaRepairDir(filepath.Dir(paths.blkFinal)); err != nil {
		return fmt.Errorf("sync replacement Block promotion: %w", err)
	}

	bw, err := block.OpenWriter(paths.blkFinal, s.shardID, blockID)
	if err != nil {
		return fmt.Errorf("reopen repaired replica Block %d: %w", blockID, err)
	}
	iw, err := block.OpenIndexWriter(paths.idxFinal)
	if err != nil {
		_ = bw.Close()
		return fmt.Errorf("reopen repaired replica index %d: %w", blockID, err)
	}
	if bw.Offset() != wantOffset {
		_ = iw.Close()
		_ = bw.Close()
		return fmt.Errorf("repaired replica offset %d, want %d", bw.Offset(), wantOffset)
	}

	s.blockWriter = bw
	s.idxWriter = iw
	return nil
}

// reopenReplicaWritersBestEffort restores usable writers after a failed
// promotion. If reopening also fails, the writers are left nil so later
// appends fail loudly on the nil check instead of on closed file handles.
func (s *Shard) reopenReplicaWritersBestEffort(paths replicaRepairPaths, blockID uint64) {
	s.blockWriter = nil
	s.idxWriter = nil
	bw, err := block.OpenWriter(paths.blkFinal, s.shardID, blockID)
	if err != nil {
		s.logger.Error("shard: reopen replica block after failed repair promotion", "block_id", blockID, "err", err)
		return
	}
	iw, err := block.OpenIndexWriter(paths.idxFinal)
	if err != nil {
		_ = bw.Close()
		s.logger.Error("shard: reopen replica index after failed repair promotion", "block_id", blockID, "err", err)
		return
	}
	s.blockWriter = bw
	s.idxWriter = iw
}

func cleanupReplicaRepairStaging(paths replicaRepairPaths) error {
	for _, path := range []string{paths.blkStaged, paths.idxStaged, paths.blkStaged + ".tmp", paths.idxStaged + ".tmp"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove replica repair staging %s: %w", path, err)
		}
	}
	return nil
}

func writeSyncedReplicaRepairFile(path string, data []byte) error {
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is derived from controlled Shard and Block IDs.
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return syncReplicaRepairDir(filepath.Dir(path))
}

func syncReplicaRepairFile(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is derived from controlled Shard and Block IDs.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func syncReplicaRepairDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // path is derived from controlled Shard and Block IDs.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
