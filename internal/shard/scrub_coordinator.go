package shard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/scrub"
)

// maxRetainedScrubResults bounds the per-coordinator scrub result cache. The
// most recent results are retained by scrub ID; the oldest is evicted first
// once the cap is exceeded (bounded memory per NFR-1).
const maxRetainedScrubResults = 16

// ErrScrubInProgress is returned when a consistency check is proposed for a
// scrub ID that already has an in-flight proposal. The raw scrub ID is not
// embedded to keep operator-facing errors free of raw identifiers.
var ErrScrubInProgress = errors.New("shard: consistency check already in progress")

// scrubCore is the narrow interface the scrub coordinator uses to reach the Shard's
// projection-authority core. Projection hashing stays under Shard.mu; scrub
// proposal tracking, result caching, and scrubber lifecycle live here.
type scrubCore interface {
	Propose(ctx context.Context, data []byte) error
	IsLeader() bool
	requireLeader() error
	scrubProjectionResult(scrubID string, entryIndex uint64) scrub.Result
	currentOpenBlockID() uint64
	RestoreBlockForRepair(ctx context.Context, blockID uint64) error
	recordEvictionHealthBlockBestEffort(blockID uint64)
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
	results     map[string]scrub.Result
	resultOrder []string
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
		results:         make(map[string]scrub.Result),
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
		c.deepScrub = scrub.NewDeep(scrub.DeepConfig{
			BlockLister:          c,
			BlockVerifier:        c,
			QuarantineManager:    c,
			Metrics:              cfg.DeepMetrics,
			LatencySignal:        cfg.LatencySignal,
			BlockRepairer:        c.defaultBlockRepairer(cfg),
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

func (c *scrubCoordinator) defaultBlockRepairer(cfg Config) scrub.BlockRepairer {
	if cfg.BlockRepairer != nil {
		return cfg.BlockRepairer
	}
	backendRestorer := c.backendRepairRestorer(cfg)
	if cfg.BlockTransferer == nil && backendRestorer == nil {
		return nil
	}
	return scrub.NewBlockRepair(scrub.BlockRepairConfig{
		BlocksDir:       c.blocksDir,
		ShardID:         cfg.ShardID,
		Transferer:      cfg.BlockTransferer,
		BackendRestorer: backendRestorer,
		Metrics:         cfg.DeepMetrics,
		PauseController: c.pauseController,
		Logger:          c.baseLogger.With("component", "block_repair"),
		PeerAddrs:       cfg.PeerAddrs,
	})
}

func (c *scrubCoordinator) backendRepairRestorer(cfg Config) scrub.BackendBlockRestorer {
	if cfg.Upload.Backend != nil {
		return c.core
	}
	return nil
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

func (c *scrubCoordinator) RunLightScrub(ctx context.Context) error {
	if c == nil || c.lightScrub == nil {
		return errors.New("shard: light scrub is not configured")
	}
	return c.lightScrub.RunOnce(ctx)
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
	if _, inFlight := c.proposals[scrubID]; inFlight {
		c.mu.Unlock()
		return scrub.Result{}, ErrScrubInProgress
	}
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
	c.storeResultLocked(result)
	ch := c.proposals[result.ScrubID]
	delete(c.proposals, result.ScrubID)
	c.mu.Unlock()

	// Send outside the lock: the coordinator must never hold c.mu across a
	// potentially blocking channel send.
	if ch != nil {
		ch <- result
	}
}

// storeResultLocked records a scrub result keyed by scrub ID, retaining at most
// maxRetainedScrubResults most-recent results and evicting the oldest first.
// Callers must hold c.mu.
func (c *scrubCoordinator) storeResultLocked(result scrub.Result) {
	if _, exists := c.results[result.ScrubID]; !exists {
		c.resultOrder = append(c.resultOrder, result.ScrubID)
		for len(c.resultOrder) > maxRetainedScrubResults {
			oldest := c.resultOrder[0]
			c.resultOrder = c.resultOrder[1:]
			delete(c.results, oldest)
		}
	}
	c.results[result.ScrubID] = result
}

func (c *scrubCoordinator) GetScrubResult(scrubID string) (scrub.Result, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result, ok := c.results[scrubID]
	return result, ok
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
