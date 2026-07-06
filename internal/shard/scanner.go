package shard

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

type ScannerConfig struct {
	Engine                   avscan.Engine
	SignatureVersionProvider avscan.SignatureVersionProvider
	Metrics                  avscan.Metrics
	Interval                 time.Duration
	IORate                   int64
}

func (c ScannerConfig) enabled() bool {
	return c.Engine != nil || c.Metrics != nil
}

func (c ScannerConfig) ioRate() int64 {
	if c.IORate > 0 {
		return c.IORate
	}
	return scrub.DefaultDeepScrubIORate
}

type scannerCore interface {
	IsLeader() bool
	currentOpenBlockID() uint64
}

type scannerCoordinator struct {
	core      scannerCore
	blocksDir string
	logger    *slog.Logger
	scheduler *avscan.Scheduler
}

func newScannerCoordinator(
	core scannerCore,
	blocksDir string,
	progressStore avscan.ProgressStore,
	shardID uint64,
	cfg ScannerConfig,
	pauseController scrub.PauseController,
	logger *slog.Logger,
) *scannerCoordinator {
	c := &scannerCoordinator{
		core:      core,
		blocksDir: blocksDir,
		logger:    logger,
	}
	if !cfg.enabled() {
		return c
	}
	c.scheduler = avscan.NewScheduler(avscan.Config{
		ShardID:                  shardID,
		BlockLister:              c,
		LeaderChecker:            core,
		Engine:                   cfg.Engine,
		ProgressStore:            progressStore,
		SignatureVersionProvider: cfg.SignatureVersionProvider,
		DetectionReporter:        c,
		Metrics:                  cfg.Metrics,
		PauseController:          pauseController,
		IOBudget:                 scrub.NewTokenBucket(cfg.ioRate()),
		Interval:                 cfg.Interval,
	})
	return c
}

func (c *scannerCoordinator) Start() {
	if c == nil || c.scheduler == nil {
		return
	}
	c.scheduler.Start(context.Background())
}

func (c *scannerCoordinator) Stop() {
	if c == nil || c.scheduler == nil {
		return
	}
	c.scheduler.Stop()
}

func (c *scannerCoordinator) Notify() {
	if c == nil || c.scheduler == nil {
		return
	}
	c.scheduler.Notify()
}

func (c *scannerCoordinator) Snapshot() avscan.Snapshot {
	if c == nil || c.scheduler == nil {
		return avscan.Snapshot{}
	}
	return c.scheduler.Snapshot()
}

func (c *scannerCoordinator) ListSealedBlocks(ctx context.Context) ([]avscan.Block, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	openBlockID := c.core.currentOpenBlockID()
	blocks, err := block.ListSealedBlocks(c.blocksDir, openBlockID, c.logger)
	if err != nil {
		return nil, err
	}
	out := make([]avscan.Block, 0, len(blocks))
	for _, info := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		size, err := blockSize(info.BlkPath)
		if err != nil {
			// A Block evicted between the listing and this stat is not an
			// error for the scan cycle; it simply has no local bytes to scan.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("shard: stat scanner Block %d: %w", info.BlockID, err)
		}
		out = append(out, avscan.Block{
			BlockID:   info.BlockID,
			SizeBytes: size,
			Open:      scannerBlockOpener(info.BlockID, info.BlkPath),
			Restored:  restoreMarkerPresent(c.blocksDir, info.BlockID),
		})
	}
	return out, nil
}

func (c *scannerCoordinator) ReportDetections(ctx context.Context, block avscan.Block, detections []avscan.Detection) error {
	reporter, ok := c.core.(avscan.DetectionReporter)
	if !ok {
		return avscan.ErrDetectionReporterUnavailable
	}
	return reporter.ReportDetections(ctx, block, detections)
}

func restoreMarkerPresent(blocksDir string, blockID uint64) bool {
	_, err := os.Stat(RestoreMarkerPath(blocksDir, blockID))
	return err == nil
}

func scannerBlockOpener(blockID uint64, path string) func(context.Context) (io.ReadCloser, error) {
	return func(ctx context.Context) (io.ReadCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.Open(path) //nolint:gosec // path comes from Shard-owned sealed Block discovery under blocksDir.
		if err != nil {
			return nil, fmt.Errorf("shard: open scanner Block %d: %w: %w", blockID, avscan.ErrBlockSource, fsErrCause(err))
		}
		return file, nil
	}
}

func blockSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *Shard) ContentScannerSnapshot() avscan.Snapshot {
	if s == nil || s.scanner == nil {
		return avscan.Snapshot{}
	}
	return s.scanner.Snapshot()
}

var _ avscan.BlockLister = (*scannerCoordinator)(nil)
