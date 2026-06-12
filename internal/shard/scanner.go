package shard

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/scrub"
)

type ScannerConfig struct {
	Engine   avscan.Engine
	Metrics  avscan.Metrics
	Interval time.Duration
	IORate   int64
}

func (c ScannerConfig) enabled() bool {
	return c.Engine != nil
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
	scheduler *avscan.Scheduler
}

func newScannerCoordinator(core scannerCore, blocksDir string, shardID uint64, cfg ScannerConfig, pauseController scrub.PauseController) *scannerCoordinator {
	c := &scannerCoordinator{
		core:      core,
		blocksDir: blocksDir,
	}
	if !cfg.enabled() {
		return c
	}
	c.scheduler = avscan.NewScheduler(avscan.Config{
		ShardID:         shardID,
		BlockLister:     c,
		LeaderChecker:   core,
		Engine:          cfg.Engine,
		Metrics:         cfg.Metrics,
		PauseController: pauseController,
		IOBudget:        scrub.NewTokenBucket(cfg.ioRate()),
		Interval:        cfg.Interval,
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

func (c *scannerCoordinator) ListSealedBlocks(_ context.Context) ([]avscan.Block, error) {
	openBlockID := c.core.currentOpenBlockID()
	blocks, err := block.ListSealedBlocks(c.blocksDir, openBlockID)
	if err != nil {
		return nil, err
	}
	out := make([]avscan.Block, 0, len(blocks))
	for _, info := range blocks {
		size, err := blockSize(info.BlkPath)
		if err != nil {
			return nil, fmt.Errorf("shard: stat scanner Block %d: %w", info.BlockID, err)
		}
		out = append(out, avscan.Block{
			BlockID:   info.BlockID,
			BlkPath:   info.BlkPath,
			IdxPath:   info.IdxPath,
			SizeBytes: size,
		})
	}
	return out, nil
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
