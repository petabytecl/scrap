package shard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/scrub"
)

// scrubCore is the narrow seam the scrub coordinator uses to reach the Shard's
// projection-authority core. Projection hashing stays under Shard.mu; scrub
// proposal tracking, result caching, and scrubber lifecycle live here.
type scrubCore interface {
	Propose(ctx context.Context, data []byte) error
	IsLeader() bool
	requireLeader() error
	scrubProjectionResult(scrubID string, entryIndex uint64) scrub.Result
	currentOpenBlockID() uint64
	RestoreBlockForRepair(ctx context.Context, blockID uint64) error
}

type scrubCoordinator struct {
	core            scrubCore
	blocksDir       string
	baseLogger      *slog.Logger
	pauseController scrub.PauseController

	cancel      context.CancelFunc
	lightScrub  *scrub.Light
	deepScrub   *scrub.Deep
	mu          sync.RWMutex
	proposals   map[string]chan scrub.Result
	scrubResult *scrub.Result
}

func newScrubCoordinator(core scrubCore, blocksDir string, baseLogger *slog.Logger, pauseController scrub.PauseController) *scrubCoordinator {
	if baseLogger == nil {
		baseLogger = slog.Default()
	}
	return &scrubCoordinator{
		core:            core,
		blocksDir:       blocksDir,
		baseLogger:      baseLogger,
		pauseController: pauseController,
		proposals:       make(map[string]chan scrub.Result),
	}
}

func (c *scrubCoordinator) Start(cfg Config) {
	if c == nil || !cfg.Scrub.Enabled {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	if cfg.ConsistencyChecker != nil && cfg.Metrics != nil {
		c.lightScrub = scrub.NewLight(scrub.LightConfig{
			Proposer:           c,
			ConsistencyChecker: cfg.ConsistencyChecker,
			LeaderChecker:      c,
			Metrics:            cfg.Metrics,
			Rebuilder:          cfg.Rebuilder,
			Logger:             c.baseLogger.With("component", "scrub"),
			PeerAddrs:          cfg.PeerAddrs,
			Interval:           cfg.Scrub.LightScrubInterval,
			Jitter:             cfg.Scrub.Jitter,
		})
		c.lightScrub.Start(ctx)
	}
	if cfg.DeepMetrics != nil {
		blockRepairer := cfg.BlockRepairer
		if blockRepairer == nil && cfg.BlockTransferer != nil {
			blockRepairer = scrub.NewBlockRepair(scrub.BlockRepairConfig{
				BlocksDir:       c.blocksDir,
				Transferer:      cfg.BlockTransferer,
				BackendRestorer: c.core,
				Metrics:         cfg.DeepMetrics,
				PauseController: c.pauseController,
				Logger:          c.baseLogger.With("component", "block_repair"),
				PeerAddrs:       cfg.PeerAddrs,
			})
		}
		c.deepScrub = scrub.NewDeep(scrub.DeepConfig{
			BlockLister:          c,
			BlockVerifier:        c,
			QuarantineManager:    c,
			Metrics:              cfg.DeepMetrics,
			LatencySignal:        cfg.LatencySignal,
			BlockRepairer:        blockRepairer,
			BlockStateClassifier: c,
			PauseController:      c.pauseController,
			Logger:               c.baseLogger.With("component", "deep_scrub"),
			IOBudget:             scrub.NewTokenBucket(cfg.Scrub.DeepScrubIORate),
			CorruptCap:           cfg.Scrub.CorruptCap,
			PauseThreshold:       cfg.Scrub.PauseLatency,
			PauseCooldown:        cfg.Scrub.PauseCooldown,
			Interval:             cfg.Scrub.DeepScrubInterval,
			Jitter:               cfg.Scrub.Jitter,
		})
		c.deepScrub.Start(ctx)
	}
}

// Stop cancels scrub scheduling and waits for owned scrubbers to finish. It
// leaves proposal channels open so an in-flight apply callback after Stop can
// still deliver to a buffered channel without panicking.
func (c *scrubCoordinator) Stop() {
	if c == nil {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.lightScrub != nil {
		c.lightScrub.Stop()
	}
	if c.deepScrub != nil {
		c.deepScrub.Stop()
	}
}

func (c *scrubCoordinator) IsLeader() bool {
	return c.core.IsLeader()
}

func (c *scrubCoordinator) ProposeConsistencyCheck(ctx context.Context, scrubID string) (scrub.Result, error) {
	if err := c.core.requireLeader(); err != nil {
		return scrub.Result{}, err
	}

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_ConsistencyCheck{
			ConsistencyCheck: &scrapv1.RequestConsistencyCheck{
				ScrubId:       scrubID,
				RequestedAtUs: time.Now().UnixMicro(),
			},
		},
	}

	data, err := proto.Marshal(cmd)
	if err != nil {
		return scrub.Result{}, fmt.Errorf("shard: marshal consistency check: %w", err)
	}

	doneCh := make(chan scrub.Result, 1)
	c.mu.Lock()
	c.proposals[scrubID] = doneCh
	c.mu.Unlock()

	if err := c.core.Propose(ctx, data); err != nil {
		c.removeProposal(scrubID)
		return scrub.Result{}, fmt.Errorf("shard: propose consistency check: %w", err)
	}

	select {
	case result := <-doneCh:
		return result, nil
	case <-ctx.Done():
		c.removeProposal(scrubID)
		return scrub.Result{}, ctx.Err()
	}
}

func (c *scrubCoordinator) applyConsistencyCheck(cc *scrapv1.RequestConsistencyCheck, entryIndex uint64) {
	result := c.core.scrubProjectionResult(cc.GetScrubId(), entryIndex)

	c.mu.Lock()
	c.scrubResult = &result
	if ch, ok := c.proposals[result.ScrubID]; ok {
		ch <- result
		delete(c.proposals, result.ScrubID)
	}
	c.mu.Unlock()
}

func (c *scrubCoordinator) GetScrubResult(scrubID string) (scrub.Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.scrubResult == nil || c.scrubResult.ScrubID != scrubID {
		return scrub.Result{}, false
	}
	return *c.scrubResult, true
}

func (c *scrubCoordinator) removeProposal(scrubID string) {
	c.mu.Lock()
	delete(c.proposals, scrubID)
	c.mu.Unlock()
}

var (
	_ scrub.Proposer      = (*scrubCoordinator)(nil)
	_ scrub.ResultCache   = (*scrubCoordinator)(nil)
	_ scrub.LeaderChecker = (*scrubCoordinator)(nil)
)
