package scrub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/block"
)

type BlockLister interface {
	ListSealedBlocks(openBlockID uint64) ([]block.Info, error)
}

type BlockVerifier interface {
	VerifyBlock(blkPath, idxPath string) (block.VerifyResult, error)
}

type QuarantineManager interface {
	Quarantine(blkPath string) error
}

type PauseController interface {
	IsPaused() bool
	Wait(ctx context.Context) error
}

type CheckpointStore interface {
	GetDeepScrubCheckpoint() (uint64, bool)
	SetDeepScrubCheckpoint(blockID uint64)
	ClearDeepScrubCheckpoint()
}

type DeepMetrics interface {
	RecordDeepRun(result string, durationSec float64)
	RecordFramesVerified(n uint64)
	RecordCorruption(corruptionType string)
	RecordQuarantine()
	SetBadDiskSuspected(v bool)
	RecordPause()
	SetProgressRatio(v float64)
	RecordRepair(result string)
	DecrementQuarantined()
	RecordSkip(reason string)
	// RecordDegradedRead counts frames that failed an I/O-class read and then
	// read back clean on the bounded re-read (#470): the Block is intact but
	// the device faulted, which operators should see without a quarantine.
	RecordDegradedRead(n uint64)
}

type BlockLocalState string

const (
	BlockLocalStateHot              BlockLocalState = "hot"
	BlockLocalStateEvicted          BlockLocalState = "evicted"
	BlockLocalStateHotCleanupNeeded BlockLocalState = "hot_cleanup_needed"
	BlockLocalStateMetadataLoss     BlockLocalState = "metadata_loss"
	BlockLocalStateUnexpectedLoss   BlockLocalState = "unexpected_loss"

	SkipReasonEvicted        = "evicted"
	SkipReasonMetadataLoss   = "metadata_loss"
	SkipReasonUnexpectedLoss = "unexpected_loss"

	// corruptionTypeIdx labels quarantines caused by a corrupt .idx side file
	// rather than corrupt .blk bytes (block.CorruptionType covers only frame
	// and Document corruption inside the .blk).
	corruptionTypeIdx = "idx"
)

type BlockStateClassifier interface {
	ClassifyScrubBlock(blockID uint64) (BlockLocalState, error)
}

type DeepConfig struct {
	BlockLister          BlockLister
	BlockVerifier        BlockVerifier
	QuarantineManager    QuarantineManager
	Metrics              DeepMetrics
	Checkpoint           CheckpointStore
	LatencySignal        LatencySignal
	BlockRepairer        BlockRepairer
	BlockStateClassifier BlockStateClassifier
	PauseController      PauseController
	Logger               *slog.Logger
	IOBudget             *TokenBucket
	OpenBlockID          uint64
	CorruptCap           int
	PauseThreshold       time.Duration
	PauseCooldown        time.Duration
	Interval             time.Duration
	Jitter               float64
}

type Deep struct {
	cfg    DeepConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewDeep(cfg DeepConfig) *Deep {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultDeepScrubInterval
	}
	if cfg.CorruptCap <= 0 {
		cfg.CorruptCap = DefaultCorruptCap
	}
	if cfg.PauseThreshold <= 0 {
		cfg.PauseThreshold = DefaultPauseLatency
	}
	if cfg.PauseCooldown <= 0 {
		cfg.PauseCooldown = DefaultPauseCooldown
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Deep{cfg: cfg}
}

func (ds *Deep) Start(ctx context.Context) {
	ctx, ds.cancel = context.WithCancel(ctx)
	ds.wg.Add(1)
	go ds.loop(ctx)
}

func (ds *Deep) Stop() {
	if ds.cancel != nil {
		ds.cancel()
	}
	ds.wg.Wait()
}

func (ds *Deep) loop(ctx context.Context) {
	defer ds.wg.Done()

	for {
		delay := ds.jitteredInterval()
		select {
		case <-time.After(delay):
			_ = ds.RunOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (ds *Deep) jitteredInterval() time.Duration {
	if ds.cfg.Jitter <= 0 {
		return ds.cfg.Interval
	}
	jitter := ds.cfg.Jitter * float64(ds.cfg.Interval)
	offset := (rand.Float64()*2 - 1) * jitter //nolint:gosec // jitter is not security-sensitive
	return ds.cfg.Interval + time.Duration(offset)
}

func (ds *Deep) RunOnce(ctx context.Context) error {
	if ds.cfg.BlockRepairer != nil {
		ds.cfg.BlockRepairer.RepairQuarantined(ctx)
	}

	start := time.Now()

	blocks, err := ds.cfg.BlockLister.ListSealedBlocks(ds.cfg.OpenBlockID)
	if err != nil {
		ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
		return err
	}

	// Clear any prior bad-disk suspicion only once the listing succeeds; a run
	// that re-encounters over-cap corruption sets it again below, so the gauge
	// reflects current disk health instead of latching until restart. Clearing
	// before the listing would reset the latched gauge to healthy on every cycle
	// even while an actual disk failure keeps the listing itself failing.
	ds.cfg.Metrics.SetBadDiskSuspected(false)

	blocks = ds.filterFromCheckpoint(blocks)

	quarantinedThisRun, err := ds.scrubBlocks(ctx, blocks)
	if err != nil {
		ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
		return err
	}

	ds.clearCheckpoint()
	ds.cfg.Metrics.RecordDeepRun("ok", time.Since(start).Seconds())
	// End-of-run repair pass (#470): a Block quarantined mid-cycle would
	// otherwise wait a full scrub interval (default a week) for its first
	// repair attempt while reads return ErrDataLoss. Gated on this run having
	// quarantined something so persistent repair failures are not retried
	// twice per cycle.
	if quarantinedThisRun > 0 && ds.cfg.BlockRepairer != nil {
		ds.cfg.BlockRepairer.RepairQuarantined(ctx)
	}
	return nil
}

// scrubBlocks verifies each listed Block in order and reports how many were
// quarantined this run.
func (ds *Deep) scrubBlocks(ctx context.Context, blocks []block.Info) (int, error) {
	total := len(blocks)
	quarantinedThisRun := 0
	for i, blk := range blocks {
		if ctx.Err() != nil {
			return quarantinedThisRun, ctx.Err()
		}
		quarantined, err := ds.scrubOneBlock(ctx, blk)
		if err != nil {
			return quarantinedThisRun, err
		}
		if quarantined {
			quarantinedThisRun++
		}
		ds.saveCheckpoint(blk.BlockID)
		ds.cfg.Metrics.SetProgressRatio(float64(i+1) / float64(total))
	}
	return quarantinedThisRun, nil
}

// scrubOneBlock waits out backpressure, applies the lifecycle skip, and
// verifies one Block, reporting whether it was quarantined.
func (ds *Deep) scrubOneBlock(ctx context.Context, blk block.Info) (bool, error) {
	if err := ds.waitPressurePause(ctx); err != nil {
		return false, err
	}
	ds.pauseIfLatencyExceeded(ctx)
	skipped, err := ds.skipBlockByLifecycle(blk)
	if err != nil || skipped {
		return false, err
	}
	if err := ds.waitIOBudget(ctx, blk.BlkPath); err != nil {
		return false, err
	}
	return ds.verifyOneBlock(blk)
}

func (ds *Deep) skipBlockByLifecycle(blk block.Info) (bool, error) {
	if ds.cfg.BlockStateClassifier == nil {
		return false, nil
	}
	state, err := ds.cfg.BlockStateClassifier.ClassifyScrubBlock(blk.BlockID)
	if err != nil {
		return false, fmt.Errorf("scrub: classify Block %d: %w", blk.BlockID, err)
	}
	switch state {
	case BlockLocalStateHot, BlockLocalStateHotCleanupNeeded:
		return false, nil
	case BlockLocalStateEvicted:
		ds.cfg.Metrics.RecordSkip(SkipReasonEvicted)
		ds.cfg.Logger.Info("scrub: skip Block", "block_id", blk.BlockID, "reason", SkipReasonEvicted)
		return true, nil
	case BlockLocalStateMetadataLoss, BlockLocalStateUnexpectedLoss:
		// A loss-state block has no verifiable index, so it cannot be CRC/SHA
		// checked regardless. Skip it (with an observable reason) and advance the
		// checkpoint rather than aborting the whole run — otherwise every
		// higher-ID block stays unverified until the condition clears.
		reason := skipReasonForLossState(state)
		ds.cfg.Metrics.RecordSkip(reason)
		ds.cfg.Logger.Warn("scrub: skip Block", "block_id", blk.BlockID, "reason", reason)
		return true, nil
	default:
		return false, fmt.Errorf("scrub: Block %d unknown local state %s", blk.BlockID, state)
	}
}

func skipReasonForLossState(state BlockLocalState) string {
	if state == BlockLocalStateUnexpectedLoss {
		return SkipReasonUnexpectedLoss
	}
	return SkipReasonMetadataLoss
}

func (ds *Deep) filterFromCheckpoint(blocks []block.Info) []block.Info {
	if ds.cfg.Checkpoint == nil {
		return blocks
	}
	lastID, ok := ds.cfg.Checkpoint.GetDeepScrubCheckpoint()
	if !ok || lastID == 0 {
		return blocks
	}
	for i, blk := range blocks {
		if blk.BlockID > lastID {
			return blocks[i:]
		}
	}
	return nil
}

func (ds *Deep) saveCheckpoint(blockID uint64) {
	if ds.cfg.Checkpoint != nil {
		ds.cfg.Checkpoint.SetDeepScrubCheckpoint(blockID)
	}
}

func (ds *Deep) clearCheckpoint() {
	if ds.cfg.Checkpoint != nil {
		ds.cfg.Checkpoint.ClearDeepScrubCheckpoint()
	}
}

func (ds *Deep) waitPressurePause(ctx context.Context) error {
	if ds.cfg.PauseController == nil || !ds.cfg.PauseController.IsPaused() {
		return nil
	}
	ds.cfg.Metrics.RecordPause()
	return ds.cfg.PauseController.Wait(ctx)
}

func (ds *Deep) pauseIfLatencyExceeded(ctx context.Context) {
	if ds.cfg.LatencySignal == nil {
		return
	}
	if !ShouldPause(ds.cfg.LatencySignal, ds.cfg.PauseThreshold) {
		return
	}
	ds.cfg.Metrics.RecordPause()
	t := time.NewTimer(ds.cfg.PauseCooldown)
	select {
	case <-t.C:
	case <-ctx.Done():
		t.Stop()
	}
}

func (ds *Deep) waitIOBudget(ctx context.Context, blkPath string) error {
	if ds.cfg.IOBudget == nil {
		return nil
	}
	info, err := os.Stat(blkPath)
	if err != nil {
		return fmt.Errorf("scrub: stat block for IO budget: %w", fsErrCause(err))
	}
	return ds.cfg.IOBudget.Wait(ctx, info.Size())
}

// verifyOneBlock verifies one Block and reports whether it was quarantined.
func (ds *Deep) verifyOneBlock(blk block.Info) (bool, error) {
	result, err := ds.cfg.BlockVerifier.VerifyBlock(blk.BlkPath, blk.IdxPath)
	if errors.Is(err, block.ErrIdxCorrupt) {
		// OpenIndexReader wraps transient .idx read faults in ErrIdxCorrupt
		// too, so give the index the same bounded re-read the frames get
		// before treating the failure as durable corruption (#470).
		result, err = ds.cfg.BlockVerifier.VerifyBlock(blk.BlkPath, blk.IdxPath)
		if err == nil {
			ds.cfg.Metrics.RecordDegradedRead(1)
			ds.cfg.Logger.Warn("scrub: Block index read back clean after transient read fault", "block_id", blk.BlockID)
		}
	}
	if errors.Is(err, block.ErrIdxCorrupt) {
		// A corrupt .idx is quarantine-eligible like corrupt .blk bytes (#470):
		// repair replaces both files from a peer or the Backend. Aborting here
		// wedged every deep-scrub run at this Block, so all higher-ID Blocks
		// lost coverage and the Block was never repaired. The .blk may be
		// intact; quarantining it anyway is the accepted trade (owner decision
		// on #470) because repair restores both files together.
		ds.cfg.Metrics.RecordCorruption(corruptionTypeIdx)
		ds.cfg.Logger.Warn("scrub: quarantining Block with corrupt index", "block_id", blk.BlockID, "err", err)
		return true, ds.quarantineBlock(blk)
	}
	if err != nil {
		return false, err
	}

	ds.cfg.Metrics.RecordFramesVerified(result.FramesVerified)
	if result.TransientReadRetries > 0 {
		// Clean after a bounded re-read: not corruption, but the device
		// faulted. Surface it so a dying disk is visible before it corrupts.
		ds.cfg.Metrics.RecordDegradedRead(result.TransientReadRetries)
		ds.cfg.Logger.Warn("scrub: Block read back clean after transient read fault",
			"block_id", blk.BlockID, "retries", result.TransientReadRetries)
	}

	if len(result.CorruptFrames) == 0 {
		return false, nil
	}

	for _, cf := range result.CorruptFrames {
		ds.cfg.Metrics.RecordCorruption(string(cf.Type))
	}
	if len(result.CorruptFrames) > ds.cfg.CorruptCap {
		ds.cfg.Metrics.SetBadDiskSuspected(true)
	}
	return true, ds.quarantineBlock(blk)
}

func (ds *Deep) quarantineBlock(blk block.Info) error {
	if err := ds.cfg.QuarantineManager.Quarantine(blk.BlkPath); err != nil {
		return fmt.Errorf("scrub: quarantine block: %w", fsErrCause(err))
	}
	ds.cfg.Metrics.RecordQuarantine()
	return nil
}
