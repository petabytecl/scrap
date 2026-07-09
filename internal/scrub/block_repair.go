package scrub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/localblock"
)

const repairStagingSuffix = ".repair"

var (
	ErrPeerBlockEvicted        = errors.New("scrub: peer Block evicted")
	ErrPeerBlockMetadataLoss   = errors.New("scrub: peer Block metadata loss")
	ErrPeerBlockUnexpectedLoss = errors.New("scrub: peer Block unexpected loss")
	ErrPeerBlockQuarantined    = errors.New("scrub: peer Block quarantined")
)

// BlockTransferer streams a peer Block into durable staging files.
// Callers must supply empty staging paths; the transferer writes and fsyncs
// both components without buffering the full Block in memory (ADR 0036 / H-13).
type BlockTransferer interface {
	TransferBlockToFiles(ctx context.Context, addr string, shardID, blockID uint64, blkPath, idxPath string) error
}

type BackendBlockRestorer interface {
	RestoreBlockForRepair(ctx context.Context, blockID uint64) error
}

type BlockRepairer interface {
	RepairQuarantined(ctx context.Context)
}

type BlockRepairConfig struct {
	BlocksDir       string
	ShardID         uint64
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

// verifyStagedReplacement confirms a fetched replacement is actually this
// Shard's Block blockID and is structurally sound before it is promoted.
// VerifyBlock checks CRC/SHA/frame integrity but deliberately skips header
// identity, so the VerifyHeader call is what stops a peer bug or version skew
// from installing a different, internally-consistent Block under blockID's name
// (every read would then fail VerifyHeader while deep scrub reports it clean).
// The Backend restore path and replica repair both perform this identity check.
func (r *BlockRepair) verifyStagedReplacement(paths blockRepairPaths, blockID uint64) error {
	if err := block.VerifyHeader(paths.blkStaged, r.cfg.ShardID, blockID); err != nil {
		return fmt.Errorf("verify replacement identity: %w", fsErrCause(err))
	}
	result, err := block.VerifyBlock(paths.blkStaged, paths.idxStaged)
	if err != nil {
		return fmt.Errorf("verify replacement: %w", fsErrCause(err))
	}
	if len(result.CorruptFrames) > 0 {
		return fmt.Errorf("verify replacement: %d corrupt frames", len(result.CorruptFrames))
	}
	return nil
}

func (r *BlockRepair) repairFromPeer(ctx context.Context, blockID uint64, peerAddr string) error {
	paths := blockRepairPathsFor(r.cfg.BlocksDir, blockID)
	if err := cleanupRepairStaging(paths); err != nil {
		return err
	}
	if err := r.cfg.Transferer.TransferBlockToFiles(ctx, peerAddr, r.cfg.ShardID, blockID, paths.blkStaged, paths.idxStaged); err != nil {
		_ = cleanupRepairStaging(paths)
		return fmt.Errorf("fetch replacement: %w", err)
	}

	if err := r.verifyStagedReplacement(paths, blockID); err != nil {
		_ = cleanupRepairStaging(paths)
		return err
	}

	if err := promoteRepairStaging(paths); err != nil {
		_ = cleanupRepairStaging(paths)
		return err
	}
	if err := r.markRepairedBlockRestored(blockID); err != nil {
		// Best-effort: the repaired Block is durably installed and structurally
		// verified. The restore marker keeps the scanner from skipping this Block
		// below an advanced frontier (peer-repaired Blocks are otherwise ordinary
		// hot files with Restored=false); a missing marker only risks a delayed
		// content scan, whereas failing the repair here would re-fetch from a peer
		// every cycle.
		r.cfg.Logger.WarnContext(ctx, "scrub: mark repaired block restored", "block_id", blockID, "err", err)
	}
	if err := removeQuarantineFiles(paths); err != nil {
		// The repaired .blk/.idx are already durably installed, so the block is
		// healthy. Failing here would mis-record a successful repair as failed
		// and re-fetch the block from a peer next cycle. Treat quarantine-marker
		// cleanup as best-effort; a leftover marker is reclaimed on a later run.
		r.cfg.Logger.WarnContext(ctx, "scrub: remove quarantine markers after repair", "block_id", blockID, "err", err)
	}
	return nil
}

// markRepairedBlockRestored records a restore marker for a peer-repaired Block
// so the content scanner keeps it eligible below an advanced watermark, matching
// the eviction-restore path. The Block is repaired from a peer, so the source is
// the peer, not the Backend.
func (r *BlockRepair) markRepairedBlockRestored(blockID uint64) error {
	return localblock.WriteRestoreMarker(r.cfg.BlocksDir, localblock.RestoreMarker{
		BlockID:      blockID,
		RestoredAtUs: time.Now().UTC().UnixMicro(),
		Source:       localblock.RestoreSourcePeer,
		Reason:       localblock.RestoreReasonRepair,
	})
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
			return fmt.Errorf("scrub: remove repair staging: %w", fsErrCause(err))
		}
	}
	return nil
}

func promoteRepairStaging(paths blockRepairPaths) error {
	// Install data before metadata: a crash between the two renames must leave
	// data-without-index (a recoverable metadata_loss shape) rather than an
	// index that references absent data.
	if err := os.Rename(paths.blkStaged, paths.blkFinal); err != nil {
		return fmt.Errorf("scrub: promote replacement block: %w", fsErrCause(err))
	}
	if err := os.Rename(paths.idxStaged, paths.idxFinal); err != nil {
		_ = os.Remove(paths.blkFinal)
		return fmt.Errorf("scrub: promote replacement index: %w", fsErrCause(err))
	}
	if err := syncDir(filepath.Dir(paths.blkFinal)); err != nil {
		return fmt.Errorf("scrub: sync promoted replacement: %w", fsErrCause(err))
	}
	return nil
}

func removeQuarantineFiles(paths blockRepairPaths) error {
	if err := os.Remove(paths.blkQ); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scrub: remove quarantined block: %w", fsErrCause(err))
	}
	if err := os.Remove(paths.idxQ); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scrub: remove quarantined index: %w", fsErrCause(err))
	}
	if err := syncDir(filepath.Dir(paths.blkQ)); err != nil {
		return fmt.Errorf("scrub: sync quarantine removal: %w", fsErrCause(err))
	}
	return nil
}

// fsErrCause strips filesystem paths from os errors so raw Block paths never
// reach Cell logs or error strings, keeping only the errno/class for diagnosis.
func fsErrCause(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err
	}
	return err
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
