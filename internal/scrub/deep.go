package scrub

import (
	"context"
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
}

type DeepConfig struct {
	BlockLister       BlockLister
	BlockVerifier     BlockVerifier
	QuarantineManager QuarantineManager
	Metrics           DeepMetrics
	Checkpoint        CheckpointStore
	LatencySignal     LatencySignal
	BlockRepairer     BlockRepairer
	PauseController   PauseController
	Logger            *slog.Logger
	IOBudget          *TokenBucket
	OpenBlockID       uint64
	CorruptCap        int
	PauseThreshold    time.Duration
	PauseCooldown     time.Duration
	Interval          time.Duration
	Jitter            float64
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

	blocks = ds.filterFromCheckpoint(blocks)
	total := len(blocks)

	for i, blk := range blocks {
		if ctx.Err() != nil {
			ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
			return ctx.Err()
		}
		if err := ds.waitPressurePause(ctx); err != nil {
			ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
			return err
		}
		ds.pauseIfLatencyExceeded(ctx)
		if err := ds.waitIOBudget(ctx, blk.BlkPath); err != nil {
			ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
			return err
		}
		if err := ds.verifyOneBlock(blk); err != nil {
			ds.cfg.Metrics.RecordDeepRun("error", time.Since(start).Seconds())
			return err
		}
		ds.saveCheckpoint(blk.BlockID)
		ds.cfg.Metrics.SetProgressRatio(float64(i+1) / float64(total))
	}

	ds.clearCheckpoint()
	ds.cfg.Metrics.RecordDeepRun("ok", time.Since(start).Seconds())
	return nil
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
		return fmt.Errorf("scrub: stat block for IO budget: %w", err)
	}
	return ds.cfg.IOBudget.Wait(ctx, info.Size())
}

func (ds *Deep) verifyOneBlock(blk block.Info) error {
	result, err := ds.cfg.BlockVerifier.VerifyBlock(blk.BlkPath, blk.IdxPath)
	if err != nil {
		return err
	}

	ds.cfg.Metrics.RecordFramesVerified(result.FramesVerified)

	if len(result.CorruptFrames) == 0 {
		return nil
	}

	for _, cf := range result.CorruptFrames {
		ds.cfg.Metrics.RecordCorruption(string(cf.Type))
	}
	if len(result.CorruptFrames) > ds.cfg.CorruptCap {
		ds.cfg.Metrics.SetBadDiskSuspected(true)
	}
	if err := ds.cfg.QuarantineManager.Quarantine(blk.BlkPath); err != nil {
		return fmt.Errorf("scrub: quarantine %s: %w", blk.BlkPath, err)
	}
	ds.cfg.Metrics.RecordQuarantine()
	return nil
}
