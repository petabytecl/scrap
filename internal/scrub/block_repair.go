package scrub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/petabytecl/scrap/internal/block"
)

const repairStagingSuffix = ".repair"

var (
	ErrPeerBlockEvicted        = errors.New("scrub: peer Block evicted")
	ErrPeerBlockMetadataLoss   = errors.New("scrub: peer Block metadata loss")
	ErrPeerBlockUnexpectedLoss = errors.New("scrub: peer Block unexpected loss")
	ErrPeerBlockQuarantined    = errors.New("scrub: peer Block quarantined")
)

type BlockTransferer interface {
	TransferBlock(ctx context.Context, addr string, blockID uint64) ([]byte, []byte, error)
}

type BackendBlockRestorer interface {
	RestoreBlockForRepair(ctx context.Context, blockID uint64) error
}

type BlockRepairer interface {
	RepairQuarantined(ctx context.Context)
}

type BlockRepairConfig struct {
	BlocksDir       string
	Transferer      BlockTransferer
	BackendRestorer BackendBlockRestorer
	Metrics         DeepMetrics
	PauseController PauseController
	Logger          *slog.Logger
	PeerAddrs       []string
}

type BlockRepair struct {
	cfg     BlockRepairConfig
	peerIdx int
}

func NewBlockRepair(cfg BlockRepairConfig) *BlockRepair {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &BlockRepair{cfg: cfg}
}

func (r *BlockRepair) RepairQuarantined(ctx context.Context) {
	if r == nil || r.cfg.BlocksDir == "" || (r.cfg.Transferer == nil && r.cfg.BackendRestorer == nil) {
		return
	}
	if err := r.waitPressurePause(ctx); err != nil {
		return
	}

	quarantined, err := block.ListQuarantined(r.cfg.BlocksDir)
	if err != nil {
		r.cfg.Logger.WarnContext(ctx, "scrub: list quarantined", "err", err)
		return
	}
	for _, blockID := range quarantined {
		if ctx.Err() != nil {
			return
		}
		if err := r.repairOneBlock(ctx, blockID); err != nil {
			r.recordRepair("failed")
			r.cfg.Logger.WarnContext(ctx, "scrub: repair quarantined block", "block_id", blockID, "err", err)
			continue
		}
		r.recordRepair("ok")
		r.decrementQuarantined()
	}
}

func (r *BlockRepair) repairOneBlock(ctx context.Context, blockID uint64) error {
	if err := r.repairFromPeers(ctx, blockID); err == nil {
		return nil
	} else if r.cfg.BackendRestorer == nil {
		return err
	}
	if err := r.cfg.BackendRestorer.RestoreBlockForRepair(ctx, blockID); err != nil {
		return fmt.Errorf("restore from Backend: %w", err)
	}
	return nil
}

func (r *BlockRepair) repairFromPeers(ctx context.Context, blockID uint64) error {
	if r.cfg.Transferer == nil || len(r.cfg.PeerAddrs) == 0 {
		return errors.New("no peer transferer configured")
	}
	var lastErr error
	for _, peer := range r.nextPeerOrder() {
		if err := r.repairFromPeer(ctx, blockID, peer); err != nil {
			lastErr = err
			r.cfg.Logger.WarnContext(ctx, "scrub: repair peer unsuitable", "block_id", blockID, "peer", peer, "err", err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all peer repairs failed: %w", lastErr)
}

func (r *BlockRepair) nextPeerOrder() []string {
	if len(r.cfg.PeerAddrs) == 0 {
		return nil
	}
	start := r.peerIdx % len(r.cfg.PeerAddrs)
	r.peerIdx++
	peers := make([]string, 0, len(r.cfg.PeerAddrs))
	for i := range r.cfg.PeerAddrs {
		peers = append(peers, r.cfg.PeerAddrs[(start+i)%len(r.cfg.PeerAddrs)])
	}
	return peers
}

func (r *BlockRepair) waitPressurePause(ctx context.Context) error {
	if r.cfg.PauseController == nil || !r.cfg.PauseController.IsPaused() {
		return nil
	}
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.RecordPause()
	}
	return r.cfg.PauseController.Wait(ctx)
}

func (r *BlockRepair) recordRepair(result string) {
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.RecordRepair(result)
	}
}

func (r *BlockRepair) decrementQuarantined() {
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.DecrementQuarantined()
	}
}

func (r *BlockRepair) repairFromPeer(ctx context.Context, blockID uint64, peerAddr string) error {
	blkData, idxData, err := r.cfg.Transferer.TransferBlock(ctx, peerAddr, blockID)
	if err != nil {
		return fmt.Errorf("fetch replacement: %w", err)
	}

	paths := blockRepairPathsFor(r.cfg.BlocksDir, blockID)
	if err := cleanupRepairStaging(paths); err != nil {
		return err
	}
	if err := atomicWrite(paths.blkStaged, blkData); err != nil {
		return fmt.Errorf("write replacement block: %w", err)
	}
	if err := atomicWrite(paths.idxStaged, idxData); err != nil {
		_ = cleanupRepairStaging(paths)
		return fmt.Errorf("write replacement index: %w", err)
	}

	result, err := block.VerifyBlock(paths.blkStaged, paths.idxStaged)
	if err != nil {
		_ = cleanupRepairStaging(paths)
		return fmt.Errorf("verify replacement: %w", err)
	}
	if len(result.CorruptFrames) > 0 {
		_ = cleanupRepairStaging(paths)
		return fmt.Errorf("verify replacement: %d corrupt frames", len(result.CorruptFrames))
	}

	if err := promoteRepairStaging(paths); err != nil {
		_ = cleanupRepairStaging(paths)
		return err
	}
	if err := removeQuarantineFiles(paths); err != nil {
		return err
	}
	return nil
}

type blockRepairPaths struct {
	blkFinal  string
	idxFinal  string
	blkStaged string
	idxStaged string
	blkQ      string
	idxQ      string
}

func blockRepairPathsFor(dir string, blockID uint64) blockRepairPaths {
	blk := block.FilePath(dir, blockID)
	idx := block.IdxFilePath(dir, blockID)
	return blockRepairPaths{
		blkFinal:  blk,
		idxFinal:  idx,
		blkStaged: blk + repairStagingSuffix,
		idxStaged: idx + repairStagingSuffix,
		blkQ:      blk + block.QuarantineSuffix,
		idxQ:      idx + block.QuarantineSuffix,
	}
}

func cleanupRepairStaging(paths blockRepairPaths) error {
	for _, path := range []string{paths.blkStaged, paths.idxStaged, paths.blkStaged + ".tmp", paths.idxStaged + ".tmp"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("scrub: remove repair staging %s: %w", path, err)
		}
	}
	return nil
}

func promoteRepairStaging(paths blockRepairPaths) error {
	if err := os.Rename(paths.idxStaged, paths.idxFinal); err != nil {
		return fmt.Errorf("scrub: promote replacement index: %w", err)
	}
	if err := os.Rename(paths.blkStaged, paths.blkFinal); err != nil {
		_ = os.Remove(paths.idxFinal)
		return fmt.Errorf("scrub: promote replacement block: %w", err)
	}
	if err := syncDir(filepath.Dir(paths.blkFinal)); err != nil {
		return fmt.Errorf("scrub: sync promoted replacement: %w", err)
	}
	return nil
}

func removeQuarantineFiles(paths blockRepairPaths) error {
	if err := os.Remove(paths.blkQ); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scrub: remove quarantined block: %w", err)
	}
	if err := os.Remove(paths.idxQ); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scrub: remove quarantined index: %w", err)
	}
	if err := syncDir(filepath.Dir(paths.blkQ)); err != nil {
		return fmt.Errorf("scrub: sync quarantine removal: %w", err)
	}
	return nil
}

func atomicWrite(destPath string, data []byte) error {
	tmp := destPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is derived from controlled Block IDs
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(destPath))
}

func syncDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // directory path is derived from controlled data directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
