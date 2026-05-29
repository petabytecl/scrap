package shard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	raftpb "go.etcd.io/raft/v3/raftpb"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/scrub"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
	"github.com/petabytecl/scrap/internal/ulid"
)

const (
	DefaultBlockSealSize  = 64 * 1024 * 1024
	DefaultBootstrapGrace = 60 * time.Second
)

type Config struct {
	DataDir        string
	ShardID        uint64
	RaftID         uint64
	Peers          map[uint64]string
	BlockSealSize  int64
	TickInterval   time.Duration
	BootstrapGrace time.Duration
	Scrub          scrub.Config
	Transport      scrapraft.Transport
	Logger         *slog.Logger

	ConsistencyChecker scrub.ConsistencyChecker
	Metrics            scrub.Metrics
	DeepMetrics        scrub.DeepMetrics
	Rebuilder          scrub.Rebuilder
	BlockRepairer      scrub.BlockRepairer
	LatencySignal      scrub.LatencySignal
	Replicator         DocumentReplicator
	PeerAddrs          []string
	Upload             UploadConfig
	WriteTelemetry     WriteStageRecorder
	IdentifierMode     telemetry.IdentifierMode
}

type Shard struct {
	dataDir        string
	blocksDir      string
	openlogDir     string
	shardID        uint64
	raftID         uint64
	peers          map[uint64]string
	idx            *index.Index
	raft           *scrapraft.Node
	replicator     DocumentReplicator
	upload         UploadConfig
	writeTelemetry WriteStageRecorder
	identifierMode telemetry.IdentifierMode
	baseLogger     *slog.Logger
	logger         *slog.Logger
	blockSealSize  int64
	raftStartedAt  time.Time
	bootstrapGrace time.Duration

	mu          sync.Mutex
	blockWriter *block.Writer
	idxWriter   *block.IndexWriter
	nextBlockID uint64

	proposalMu     sync.Mutex
	proposals      map[string]chan error
	scrubProposals map[string]chan scrub.Result

	scrubMu     sync.RWMutex
	scrubResult *scrub.Result

	scrubber                 *scrub.Light
	deepScrubber             *scrub.Deep
	scrubCancel              context.CancelFunc
	uploadCancel             context.CancelFunc
	uploadDone               chan struct{}
	uploadNotify             chan struct{}
	uploadMu                 sync.Mutex
	uploadCurrentConcurrency int
	uploadSuccesses          int
	uploadRequeued           map[uint64]struct{}
	uploadPausedUntil        time.Time
	uploadAuthPaused         bool
	uploadPressureLevel      UploadPressureLevel
	uploadPendingBytes       int64
	uploadPendingBlocks      int
	uploadPressureScrubGate  *pressurePauseGate
	orphanedSeals            []index.PendingUpload

	rebuilding  atomic.Bool
	rebuildDone atomic.Pointer[chan struct{}]
}

func (c *Config) applyDefaults() {
	if c.BlockSealSize <= 0 {
		c.BlockSealSize = DefaultBlockSealSize
	}
	if c.BootstrapGrace == 0 {
		c.BootstrapGrace = DefaultBootstrapGrace
	}
	if c.WriteTelemetry == nil {
		c.WriteTelemetry = noopWriteTelemetry{}
	}
}

func Open(cfg Config) (*Shard, error) {
	cfg.applyDefaults()

	baseLogger := cfg.Logger
	if baseLogger == nil {
		baseLogger = slog.Default()
	}
	logger := baseLogger.With("component", "shard")

	blocksDir := filepath.Join(cfg.DataDir, "blocks")
	pebbleDir := filepath.Join(cfg.DataDir, "pebble")
	openlogDir := filepath.Join(cfg.DataDir, "openlog")
	raftDir := filepath.Join(cfg.DataDir, "raft")

	if err := ensureShardDirs(blocksDir, pebbleDir, openlogDir, raftDir); err != nil {
		return nil, err
	}

	idx, err := index.Open(pebbleDir)
	if err != nil {
		return nil, fmt.Errorf("shard: open index: %w", err)
	}

	nextID, err := scanMaxBlockID(blocksDir)
	if err != nil {
		_ = idx.Close()
		return nil, err
	}

	s := &Shard{
		dataDir:                 cfg.DataDir,
		blocksDir:               blocksDir,
		openlogDir:              openlogDir,
		shardID:                 cfg.ShardID,
		raftID:                  cfg.RaftID,
		peers:                   cfg.Peers,
		idx:                     idx,
		baseLogger:              baseLogger,
		logger:                  logger,
		replicator:              cfg.Replicator,
		upload:                  cfg.Upload,
		writeTelemetry:          cfg.WriteTelemetry,
		identifierMode:          cfg.IdentifierMode,
		blockSealSize:           cfg.BlockSealSize,
		nextBlockID:             nextID,
		proposals:               make(map[string]chan error),
		scrubProposals:          make(map[string]chan scrub.Result),
		uploadNotify:            make(chan struct{}, 1),
		uploadRequeued:          make(map[uint64]struct{}),
		uploadPressureScrubGate: newPressurePauseGate(),
		raftStartedAt:           time.Now(),
		bootstrapGrace:          cfg.BootstrapGrace,
	}
	done := make(chan struct{})
	close(done)
	s.rebuildDone.Store(&done)

	if err := s.recoverOpenlog(); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("shard: openlog recovery: %w", err)
	}

	if err := s.openNewBlock(); err != nil {
		_ = idx.Close()
		return nil, fmt.Errorf("shard: open block: %w", err)
	}

	transport := cfg.Transport
	if transport == nil {
		transport = &noopTransport{}
	}
	raftNode, err := scrapraft.Open(scrapraft.Config{
		ID:           cfg.RaftID,
		Peers:        cfg.Peers,
		DataDir:      raftDir,
		TickInterval: cfg.TickInterval,
		Transport:    transport,
		Apply:        s.applyEntries,
		Logger:       baseLogger.With("component", "raft"),
	})
	if err != nil {
		s.closeBlockAndIdx()
		_ = idx.Close()
		return nil, fmt.Errorf("shard: open raft: %w", err)
	}
	s.raft = raftNode

	s.resetUploadConcurrency()
	if err := s.refreshUploadPressureLocked(); err != nil {
		raftNode.Stop()
		s.closeBlockAndIdx()
		_ = idx.Close()
		return nil, fmt.Errorf("shard: refresh upload pressure: %w", err)
	}
	s.setUploadAuthPausedMetric(false)

	s.startScrubber(cfg)
	s.startUploadProcessor()

	return s, nil
}

func ensureShardDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("shard: mkdir %s: %w", dir, err)
		}
	}
	return nil
}

func (s *Shard) startScrubber(cfg Config) {
	if !cfg.Scrub.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scrubCancel = cancel
	if cfg.ConsistencyChecker != nil && cfg.Metrics != nil {
		s.scrubber = scrub.NewLight(scrub.LightConfig{
			Proposer:           s,
			ConsistencyChecker: cfg.ConsistencyChecker,
			LeaderChecker:      s,
			Metrics:            cfg.Metrics,
			Rebuilder:          cfg.Rebuilder,
			Logger:             s.baseLogger.With("component", "scrub"),
			PeerAddrs:          cfg.PeerAddrs,
			Interval:           cfg.Scrub.LightScrubInterval,
			Jitter:             cfg.Scrub.Jitter,
		})
		s.scrubber.Start(ctx)
	}
	if cfg.DeepMetrics != nil {
		s.deepScrubber = scrub.NewDeep(scrub.DeepConfig{
			BlockLister:       s,
			BlockVerifier:     s,
			QuarantineManager: s,
			Metrics:           cfg.DeepMetrics,
			LatencySignal:     cfg.LatencySignal,
			BlockRepairer:     cfg.BlockRepairer,
			PauseController:   s.uploadPressureScrubGate,
			Logger:            s.baseLogger.With("component", "deep_scrub"),
			IOBudget:          scrub.NewTokenBucket(cfg.Scrub.DeepScrubIORate),
			PeerAddrs:         cfg.PeerAddrs,
			CorruptCap:        cfg.Scrub.CorruptCap,
			PauseThreshold:    cfg.Scrub.PauseLatency,
			PauseCooldown:     cfg.Scrub.PauseCooldown,
			Interval:          cfg.Scrub.DeepScrubInterval,
			Jitter:            cfg.Scrub.Jitter,
		})
		s.deepScrubber.Start(ctx)
	}
}

//nolint:cyclop,gocognit // orchestration function managing seal check, prep file, block append, raft propose, and apply
func (s *Shard) WriteDocument(ctx context.Context, txID, docName, contentType, idempotencyKey string, body io.Reader) (storeapi.WriteResult, error) {
	if err := s.requireWritableLeader(); err != nil {
		return storeapi.WriteResult{}, err
	}

	s.mu.Lock()

	key := txID + "\x00" + docName
	if s.docExistsInPebble(txID, docName) {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, txID, docName)
	}

	if s.blockWriter.Offset() > block.HeaderSize && s.blockWriter.Offset() >= s.blockSealSize {
		if err := s.sealAndOpenNew(ctx); err != nil {
			s.mu.Unlock()
			return storeapi.WriteResult{}, fmt.Errorf("shard: seal block: %w", err)
		}
	}

	writeID := ulid.New().String()
	blockID := s.blockWriter.BlockID()
	startOffset := s.blockWriter.Offset()

	if startOffset < 0 {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: negative start offset %d", startOffset)
	}
	prepEntry := &scrapv1.OpenlogEntry{
		TransactionId:  txID,
		DocumentName:   docName,
		BlockId:        blockID,
		StartOffset:    uint64(startOffset),
		ContentType:    contentType,
		IdempotencyKey: idempotencyKey,
	}
	if err := s.writePrepFile(writeID, prepEntry); err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: write prep: %w", err)
	}
	prepCleaned := false
	defer func() {
		if !prepCleaned {
			_ = os.Remove(s.prepPath(writeID))
		}
	}()

	now := time.Now()

	ctx, appendStage := s.writeTelemetry.StartStage(ctx, "block_append")
	var bodyCopy bytes.Buffer
	result, err := s.blockWriter.AppendDocument(txID, docName, contentType, io.TeeReader(body, &bodyCopy))
	appendStage.End(err)
	if err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: append document: %w", err)
	}

	s.mu.Unlock()

	ctx, replicateStage := s.writeTelemetry.StartStage(ctx, "peer_replicate")
	err = s.replicateDocument(ctx, prepEntry, contentType, result, bodyCopy.Bytes())
	replicateStage.End(err)
	if err != nil {
		return storeapi.WriteResult{}, err
	}

	cmd := &scrapv1.RaftCommand{
		Command: &scrapv1.RaftCommand_CommitDoc{
			CommitDoc: &scrapv1.CommitDocument{
				TransactionId:  txID,
				DocumentName:   docName,
				ContentType:    contentType,
				IdempotencyKey: idempotencyKey,
				BlockId:        blockID,
				FirstFrameOff:  uint64(max(0, result.FirstFrameOffset)),
				FrameCount:     result.FrameCount,
				TotalBytes:     result.Size,
				Sha256:         result.SHA256[:],
				CreatedAtUs:    now.UnixMicro(),
			},
		},
	}

	doneCh := make(chan error, 1)
	s.proposalMu.Lock()
	s.proposals[key] = doneCh
	s.proposalMu.Unlock()

	ctx, proposeStage := s.writeTelemetry.StartStage(ctx, "raft_propose")
	// Stamp the active propose span into the command so every voter's apply loop
	// recovers it and emits a child apply span on all replicas. See ADR 0013.
	injectTraceContext(ctx, cmd)
	data, err := proto.Marshal(cmd)
	if err != nil {
		proposeStage.End(err)
		s.proposalMu.Lock()
		delete(s.proposals, key)
		s.proposalMu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: marshal command: %w", err)
	}
	proposeErr := s.raft.Propose(ctx, data)
	proposeStage.End(proposeErr)
	if proposeErr != nil {
		s.proposalMu.Lock()
		delete(s.proposals, key)
		s.proposalMu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("shard: propose: %w", proposeErr)
	}

	_, applyStage := s.writeTelemetry.StartStage(ctx, "raft_apply")
	select {
	case applyErr := <-doneCh:
		applyStage.End(applyErr)
		if applyErr != nil {
			return storeapi.WriteResult{}, applyErr
		}
	case <-ctx.Done():
		applyStage.End(ctx.Err())
		return storeapi.WriteResult{}, ctx.Err()
	}

	prepCleaned = true
	_ = os.Remove(s.prepPath(writeID))

	return storeapi.WriteResult{
		SHA256:    result.SHA256,
		Size:      result.Size,
		CreatedAt: now,
	}, nil
}

func (s *Shard) HeadDocument(ctx context.Context, txID, docName string) (storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return storeapi.DocumentMeta{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.findDocEntry(txID, docName)
	if err != nil {
		return storeapi.DocumentMeta{}, err
	}

	return storeapi.DocumentMeta{
		Name:        entry.DocName,
		ContentType: entry.ContentType,
		Size:        entry.TotalBytes,
		SHA256:      entry.SHA256,
		CreatedAt:   entry.CreatedAt,
	}, nil
}

func (s *Shard) ReadDocument(ctx context.Context, txID, docName string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.findDocEntry(txID, docName)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}

	blkPath := s.blockPath(entry.blockID)
	rc, err := block.ReadDocument(blkPath, entry.IndexEntry)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	}

	meta := storeapi.DocumentMeta{
		Name:        entry.DocName,
		ContentType: entry.ContentType,
		Size:        entry.TotalBytes,
		SHA256:      entry.SHA256,
		CreatedAt:   entry.CreatedAt,
	}
	return rc, meta, nil
}

func (s *Shard) FindDocuments(ctx context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return nil, nil
	}

	var docs []storeapi.DocumentMeta
	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
		}
		entries := ir.FindByTransaction(txID)
		_ = ir.Close() // best-effort; data already read

		for _, e := range entries {
			docs = append(docs, storeapi.DocumentMeta{
				Name:        e.DocName,
				ContentType: e.ContentType,
				Size:        e.TotalBytes,
				SHA256:      e.SHA256,
				CreatedAt:   e.CreatedAt,
			})
		}
	}
	return docs, nil
}

func (s *Shard) IsLeader() bool {
	return s.raft.IsLeader()
}

func (s *Shard) LeaderID() uint64 {
	return s.raft.LeaderID()
}

func (s *Shard) AppliedIndex() uint64 {
	return s.raft.AppliedIndex()
}

func (s *Shard) CommitIndex() uint64 {
	return s.raft.CommitIndex()
}

// DiskStats reports local data-volume usage plus the Pebble projection size for the
// USE dashboard. Best-effort: a Statfs failure yields zeroed disk fields.
func (s *Shard) DiskStats() telemetry.DiskStats {
	stats := telemetry.DiskStats{}
	// A projection rebuild closes, nils, and replaces s.idx under s.mu; hold the lock
	// so a concurrent /metrics scrape never reads a closed or nil projection.
	s.mu.Lock()
	if s.idx != nil {
		stats.ProjectionBytes = s.idx.DiskUsageBytes()
	}
	s.mu.Unlock()
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.dataDir, &st); err != nil || st.Bsize <= 0 {
		return stats
	}
	bsize := uint64(st.Bsize)
	stats.FreeBytes = clampUint64ToInt64(st.Bavail * bsize)
	stats.UsedBytes = clampUint64ToInt64((st.Blocks - st.Bfree) * bsize)
	return stats
}

func clampUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func (s *Shard) CheckReadiness(_ context.Context) error {
	if s.rebuilding.Load() {
		return fmt.Errorf("%w: shard unavailable", storeapi.ErrRebuilding)
	}
	if s.raft.LeaderID() != 0 {
		return nil
	}
	if time.Since(s.raftStartedAt) < s.bootstrapGrace {
		return nil
	}
	return errors.New("shard: no leader elected")
}

func (s *Shard) requireLeader() error {
	if s.rebuilding.Load() {
		return fmt.Errorf("%w: shard unavailable", storeapi.ErrRebuilding)
	}
	if s.raft.IsLeader() {
		return nil
	}
	return s.notLeaderError()
}

func (s *Shard) requireWritableLeader() error {
	if err := s.requireLeader(); err != nil {
		return err
	}
	return s.rejectWriteForUploadPressure()
}

func (s *Shard) requireLeaderRead(ctx context.Context) error {
	if s.rebuilding.Load() {
		return fmt.Errorf("%w: shard unavailable", storeapi.ErrRebuilding)
	}
	if !s.raft.IsLeader() {
		return s.notLeaderError()
	}
	_, err := s.raft.ReadIndex(ctx)
	if err != nil {
		return fmt.Errorf("shard: read index: %w", err)
	}
	return nil
}

func (s *Shard) TriggerRebuild(ctx context.Context) (alreadyInProgress bool, err error) {
	if !s.rebuilding.CompareAndSwap(false, true) {
		return true, nil
	}
	done := make(chan struct{})
	s.rebuildDone.Store(&done)
	// The rebuild is detached: it outlives the triggering RPC. WithoutCancel keeps
	// the caller's trace/values for log correlation while dropping cancellation and
	// any deadline, so returning from this RPC does not abort the rebuild.
	go s.doRebuild(context.WithoutCancel(ctx), done)
	return false, nil
}

func (s *Shard) doRebuild(ctx context.Context, done chan struct{}) {
	defer close(done)

	pebbleDir := filepath.Join(s.dataDir, "pebble")
	tempDir := filepath.Join(s.dataDir, fmt.Sprintf("pebble.rebuild-%d", time.Now().UnixNano()))
	oldDir := filepath.Join(s.dataDir, fmt.Sprintf("pebble.previous-%d", time.Now().UnixNano()))

	if err := s.prepareRebuildProjection(tempDir); err != nil {
		s.logger.ErrorContext(ctx, "rebuild: prepare projection failed", "err", err)
		_ = os.RemoveAll(tempDir)
		s.rebuilding.Store(false)
		return
	}

	s.mu.Lock()
	err := s.swapRebuiltProjectionLocked(pebbleDir, tempDir, oldDir)
	idxNil := s.idx == nil
	s.mu.Unlock()

	_ = os.RemoveAll(tempDir)

	if err != nil {
		s.logger.ErrorContext(ctx, "rebuild: swap projection failed", "err", err, "shard_id", s.shardID)
		if idxNil {
			s.logger.ErrorContext(ctx, "rebuild: index is nil after failed swap; shard degraded", "err", err, "shard_id", s.shardID)
			return
		}
	}

	_ = os.RemoveAll(oldDir)
	s.rebuilding.Store(false)
}

func (s *Shard) prepareRebuildProjection(tempDir string) error {
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("shard: remove stale rebuild dir: %w", err)
	}

	newIdx, err := index.Open(tempDir)
	if err != nil {
		return fmt.Errorf("shard: open rebuild index: %w", err)
	}
	if err := s.rebuildProjectionInto(newIdx); err != nil {
		_ = newIdx.Close()
		return err
	}
	if err := newIdx.Close(); err != nil {
		return fmt.Errorf("shard: close rebuild index: %w", err)
	}
	return nil
}

func (s *Shard) swapRebuiltProjectionLocked(pebbleDir, tempDir, oldDir string) error {
	if err := s.idx.Close(); err != nil {
		return fmt.Errorf("shard: close current index: %w", err)
	}
	s.idx = nil

	if err := s.moveCurrentProjectionLocked(pebbleDir, oldDir); err != nil {
		return err
	}
	if err := s.installRebuiltProjectionLocked(pebbleDir, tempDir, oldDir); err != nil {
		return err
	}
	if err := s.reopenInstalledProjectionLocked(pebbleDir, oldDir); err != nil {
		return err
	}
	_ = os.RemoveAll(oldDir)
	return nil
}

func (s *Shard) moveCurrentProjectionLocked(pebbleDir, oldDir string) error {
	if err := os.Rename(pebbleDir, oldDir); err != nil {
		if reopenErr := s.reopenProjectionLocked(pebbleDir); reopenErr != nil {
			return fmt.Errorf("shard: rename current index: %w; reopen current index: %w", err, reopenErr)
		}
		return fmt.Errorf("shard: rename current index: %w", err)
	}
	return nil
}

func (s *Shard) installRebuiltProjectionLocked(pebbleDir, tempDir, oldDir string) error {
	if err := os.Rename(tempDir, pebbleDir); err != nil {
		_ = os.Rename(oldDir, pebbleDir)
		if reopenErr := s.reopenProjectionLocked(pebbleDir); reopenErr != nil {
			return fmt.Errorf("shard: install rebuild index: %w; reopen current index: %w", err, reopenErr)
		}
		return fmt.Errorf("shard: install rebuild index: %w", err)
	}
	return nil
}

func (s *Shard) reopenInstalledProjectionLocked(pebbleDir, oldDir string) error {
	if err := s.reopenProjectionLocked(pebbleDir); err != nil {
		_ = os.RemoveAll(pebbleDir)
		_ = os.Rename(oldDir, pebbleDir)
		if reopenErr := s.reopenProjectionLocked(pebbleDir); reopenErr != nil {
			return fmt.Errorf("shard: reopen rebuilt index: %w; restore current index: %w", err, reopenErr)
		}
		return fmt.Errorf("shard: reopen rebuilt index: %w", err)
	}
	return nil
}

func (s *Shard) reopenProjectionLocked(pebbleDir string) error {
	idx, err := index.Open(pebbleDir)
	if err != nil {
		return err
	}
	s.idx = idx
	return nil
}

func (s *Shard) rebuildProjectionInto(projection *index.Index) error {
	blockIDs, err := s.listBlockIndexIDs()
	if err != nil {
		return err
	}

	for _, blockID := range blockIDs {
		ir, err := block.OpenIndexReader(s.idxPath(blockID))
		if err != nil {
			return fmt.Errorf("shard: open block index %d: %w", blockID, err)
		}
		entries := ir.Entries()
		if err := ir.Close(); err != nil {
			return fmt.Errorf("shard: close block index %d: %w", blockID, err)
		}

		for _, entry := range entries {
			if err := addProjectionDocument(projection, entry.TransactionID, blockID); err != nil {
				return fmt.Errorf("shard: rebuild projection %s/%s: %w", entry.TransactionID, entry.DocName, err)
			}
		}
	}

	if err := s.rebuildUploadOutbox(projection, blockIDs); err != nil {
		return err
	}
	return nil
}

func (s *Shard) rebuildUploadOutbox(projection *index.Index, blockIDs []uint64) error {
	be := s.upload.Backend
	if !s.upload.Enabled || be == nil {
		return nil
	}

	s.mu.Lock()
	openBlockID := uint64(0)
	if s.blockWriter != nil {
		openBlockID = s.blockWriter.BlockID()
	}
	s.mu.Unlock()

	ctx := context.Background()
	cellID := s.uploadCellID()
	for _, blockID := range blockIDs {
		if blockID == openBlockID {
			continue
		}
		prefix := backendKeyPrefix(cellID, s.shardID, blockID)
		uploaded, err := blockFullyUploaded(ctx, be, prefix)
		if err != nil {
			s.logger.WarnContext(ctx, "shard: rebuild upload check skipped (transient)", "block_id", blockID, "err", err)
			continue
		}
		if uploaded {
			continue
		}
		blkPath := s.blockPath(blockID)
		info, statErr := os.Stat(blkPath)
		if statErr != nil {
			continue
		}
		if err := projection.PutPendingUpload(index.PendingUpload{
			BlockID:         blockID,
			ShardID:         s.shardID,
			SealedSizeBytes: info.Size(),
			SealedAtUs:      info.ModTime().UnixMicro(),
		}); err != nil {
			return fmt.Errorf("shard: rebuild pending upload %d: %w", blockID, err)
		}
	}
	return nil
}

// blockFullyUploaded returns true when both .blk and .idx exist in the backend,
// false when either is definitively missing. Returns an error for transient/auth
// failures so the caller can avoid incorrectly re-queuing already-uploaded blocks.
func blockFullyUploaded(ctx context.Context, be backend.Backend, prefix string) (bool, error) {
	for _, ext := range []string{".blk", ".idx"} {
		_, err := be.HeadObject(ctx, prefix+ext)
		if err == nil {
			continue
		}
		if errors.Is(err, backend.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Shard) listBlockIndexIDs() ([]uint64, error) {
	entries, err := os.ReadDir(s.blocksDir)
	if err != nil {
		return nil, fmt.Errorf("shard: read blocks dir: %w", err)
	}

	var blockIDs []uint64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".idx") {
			continue
		}
		hexPart := strings.TrimSuffix(name, ".idx")
		id, err := strconv.ParseUint(hexPart, 16, 64)
		if err != nil {
			continue
		}
		blockIDs = append(blockIDs, id)
	}
	sort.Slice(blockIDs, func(i, j int) bool {
		return blockIDs[i] < blockIDs[j]
	})
	return blockIDs, nil
}

func (s *Shard) WaitRebuild() {
	if p := s.rebuildDone.Load(); p != nil {
		<-*p
	}
}

func (s *Shard) DataDirForTest() string {
	return s.dataDir
}

func (s *Shard) CurrentBlockIDForTest() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockWriter == nil {
		return 0
	}
	return s.blockWriter.BlockID()
}

func (s *Shard) OrphanedSealsForTest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orphanedSeals)
}

func (s *Shard) SetRebuildingForTest(v bool) {
	s.rebuilding.Store(v)
	if v {
		done := make(chan struct{})
		s.rebuildDone.Store(&done)
	} else {
		done := make(chan struct{})
		close(done)
		s.rebuildDone.Store(&done)
	}
}

func (s *Shard) notLeaderError() error {
	leaderID := s.raft.LeaderID()
	var leaderAddr string
	if leaderID != 0 {
		if addr, ok := s.peers[leaderID]; ok {
			leaderAddr = addr
		}
	}
	return &storeapi.NotLeaderError{LeaderAddr: leaderAddr}
}

func (s *Shard) RaftStep(ctx context.Context, msg raftpb.Message) error {
	return s.raft.Step(ctx, msg)
}

func (s *Shard) Close() error {
	if s.scrubCancel != nil {
		s.scrubCancel()
	}
	if s.scrubber != nil {
		s.scrubber.Stop()
	}
	if s.deepScrubber != nil {
		s.deepScrubber.Stop()
	}
	if s.uploadCancel != nil {
		s.uploadCancel()
	}
	if s.uploadDone != nil {
		<-s.uploadDone
	}
	s.raft.Stop()
	s.WaitRebuild()

	return s.closeStorage()
}

func (s *Shard) closeStorage() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.idxWriter != nil {
		captureFirstErr(&firstErr, s.idxWriter.Close())
	}
	if s.blockWriter != nil {
		captureFirstErr(&firstErr, s.blockWriter.Close())
	}
	if s.idx != nil {
		captureFirstErr(&firstErr, s.idx.Close())
	}
	return firstErr
}

func captureFirstErr(firstErr *error, err error) {
	if err != nil && *firstErr == nil {
		*firstErr = err
	}
}

func (s *Shard) applyEntries(entries []raftpb.Entry, replayUntil uint64) error {
	for _, e := range entries {
		if e.Type != raftpb.EntryNormal || len(e.Data) == 0 {
			continue
		}

		cmd := &scrapv1.RaftCommand{}
		if err := proto.Unmarshal(e.Data, cmd); err != nil {
			return fmt.Errorf("shard: unmarshal raft command: %w", err)
		}

		// Entries at or below the replay watermark (the durably-applied index, passed
		// in by the Raft node) were applied before a restart; their trace context is
		// stale, so apply spans are suppressed. Entries above it — including ones
		// committed but not yet applied before a crash — emit live spans (ADR 0013 §3).
		live := e.Index > replayUntil
		if err := s.applyEntryTraced(cmd, e.Index, live); err != nil {
			return err
		}
	}
	return nil
}

// applyEntryTraced wraps applyEntryCommand in a per-voter apply span when the entry
// is a live commit (not startup replay) and the command is span-worthy. The span's
// parent is recovered from the command's W3C trace context, so it nests under the
// originating write trace on every replica. See ADR 0013.
func (s *Shard) applyEntryTraced(cmd *scrapv1.RaftCommand, entryIndex uint64, live bool) error {
	operation, attrs := applySpanInfo(cmd, s.identifierMode)
	if !live || operation == "" || s.writeTelemetry == nil {
		return s.applyEntryCommand(cmd, entryIndex)
	}
	opts := []trace.SpanStartOption{trace.WithAttributes(attrs...)}
	// A committed Document forward-links to its Block's upload trace, so a write can
	// be navigated to "where did these bytes go" in one click — and the link survives
	// leader failover because the block trace_id is deterministic (ADR 0013).
	if c, ok := cmd.Command.(*scrapv1.RaftCommand_CommitDoc); ok {
		opts = append(opts, trace.WithLinks(trace.Link{
			SpanContext: blockTraceContext(s.uploadCellID(), s.shardID, c.CommitDoc.GetBlockId()),
			Attributes:  []attribute.KeyValue{attribute.String("scrap.link", "block.upload")},
		}))
	}
	ctx := extractTraceContext(context.Background(), cmd)
	ctx, applyEnd := s.writeTelemetry.StartSpan(ctx, "scrap.apply/"+operation, opts...)
	err := s.applyEntryCommand(cmd, entryIndex)
	// DEBUG (Tier-1) on the apply span's context: the log line carries the same
	// trace_id/span_id, so Grafana can jump trace <-> logs for this apply (ADR 0013).
	s.logger.DebugContext(ctx, "applied raft command", "op", operation, "index", entryIndex)
	applyEnd.End(err)
	return err
}

// applySpanInfo maps a RaftCommand to an apply-span operation name and attributes.
// Identity follows mode (hashed by default, ADR 0012); scrap.block_id is a
// low-cardinality attribute used to correlate the write trace with the block.upload
// trace (ADR 0013).
func applySpanInfo(cmd *scrapv1.RaftCommand, mode telemetry.IdentifierMode) (string, []attribute.KeyValue) {
	switch c := cmd.Command.(type) {
	case *scrapv1.RaftCommand_CommitDoc:
		attrs := telemetry.DocumentIdentityAttributes(
			c.CommitDoc.GetTransactionId(),
			c.CommitDoc.GetDocumentName(),
			mode,
		)
		attrs = append(attrs, blockIDAttribute(c.CommitDoc.GetBlockId()))
		return "commit_document", attrs
	case *scrapv1.RaftCommand_SealBlock:
		return "seal_block", []attribute.KeyValue{blockIDAttribute(c.SealBlock.GetBlockId())}
	case *scrapv1.RaftCommand_ConfirmUpload:
		return "confirm_upload", []attribute.KeyValue{blockIDAttribute(c.ConfirmUpload.GetBlockId())}
	case *scrapv1.RaftCommand_ConsistencyCheck:
		return "consistency_check", nil
	}
	return "", nil
}

// blockIDAttribute renders a block ID as a clamped int64 span attribute, matching
// the uint64->int64 guard used for shard_id (avoids overflow on absurd inputs).
func blockIDAttribute(blockID uint64) attribute.KeyValue {
	v := int64(math.MaxInt64)
	if blockID <= math.MaxInt64 {
		v = int64(blockID)
	}
	return attribute.Int64("scrap.block_id", v)
}

func (s *Shard) applyEntryCommand(cmd *scrapv1.RaftCommand, entryIndex uint64) error {
	switch c := cmd.Command.(type) {
	case *scrapv1.RaftCommand_CommitDoc:
		s.applyCommitDocumentCommand(c.CommitDoc)
	case *scrapv1.RaftCommand_ConsistencyCheck:
		s.applyConsistencyCheckCommand(c.ConsistencyCheck, entryIndex)
	case *scrapv1.RaftCommand_SealBlock:
		return s.applySealBlock(c.SealBlock)
	case *scrapv1.RaftCommand_ConfirmUpload:
		return s.applyConfirmUpload(c.ConfirmUpload)
	}
	return nil
}

func (s *Shard) applyCommitDocumentCommand(doc *scrapv1.CommitDocument) {
	key := doc.TransactionId + "\x00" + doc.DocumentName
	applyErr := s.applyCommitDocument(doc)

	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()

	if ch, ok := s.proposals[key]; ok {
		ch <- applyErr
		delete(s.proposals, key)
	}
}

func (s *Shard) applyConsistencyCheckCommand(cc *scrapv1.RequestConsistencyCheck, entryIndex uint64) {
	result := s.applyConsistencyCheck(cc, entryIndex)

	s.proposalMu.Lock()
	defer s.proposalMu.Unlock()

	if ch, ok := s.scrubProposals[result.ScrubID]; ok {
		ch <- result
		delete(s.scrubProposals, result.ScrubID)
	}
}

func (s *Shard) applyConsistencyCheck(cc *scrapv1.RequestConsistencyCheck, entryIndex uint64) scrub.Result {
	s.mu.Lock()
	s.idx.SetAppliedIndex(entryIndex)
	_, hash, err := s.idx.StreamingHash()
	s.mu.Unlock()

	result := scrub.Result{
		ScrubID:      cc.ScrubId,
		AppliedIndex: entryIndex,
	}
	if err == nil {
		result.SHA256 = hash
	}

	s.scrubMu.Lock()
	s.scrubResult = &result
	s.scrubMu.Unlock()

	return result
}

func (s *Shard) ProposeConsistencyCheck(ctx context.Context, scrubID string) (scrub.Result, error) {
	if err := s.requireLeader(); err != nil {
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
	s.proposalMu.Lock()
	s.scrubProposals[scrubID] = doneCh
	s.proposalMu.Unlock()

	if err := s.raft.Propose(ctx, data); err != nil {
		s.proposalMu.Lock()
		delete(s.scrubProposals, scrubID)
		s.proposalMu.Unlock()
		return scrub.Result{}, fmt.Errorf("shard: propose consistency check: %w", err)
	}

	select {
	case result := <-doneCh:
		return result, nil
	case <-ctx.Done():
		s.proposalMu.Lock()
		delete(s.scrubProposals, scrubID)
		s.proposalMu.Unlock()
		return scrub.Result{}, ctx.Err()
	}
}

func (s *Shard) GetScrubResult(scrubID string) (scrub.Result, bool) {
	s.scrubMu.RLock()
	defer s.scrubMu.RUnlock()

	if s.scrubResult == nil || s.scrubResult.ScrubID != scrubID {
		return scrub.Result{}, false
	}
	return *s.scrubResult, true
}

func (s *Shard) applyCommitDocument(doc *scrapv1.CommitDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.docExistsInPebble(doc.TransactionId, doc.DocumentName) {
		return fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, doc.TransactionId, doc.DocumentName)
	}

	var sha [32]byte
	copy(sha[:], doc.Sha256)
	createdAt := time.UnixMicro(doc.CreatedAtUs)

	idxW := s.idxWriterForBlock(doc.BlockId)
	if idxW != nil {
		if err := idxW.Append(block.IndexEntry{
			TransactionID: doc.TransactionId,
			DocName:       doc.DocumentName,
			ContentType:   doc.ContentType,
			CreatedAt:     createdAt,
			FirstFrameOff: safeUint64ToInt64(doc.FirstFrameOff),
			FrameCount:    doc.FrameCount,
			TotalBytes:    doc.TotalBytes,
			SHA256:        sha,
		}); err != nil {
			return fmt.Errorf("shard: apply write idx: %w", err)
		}
	}

	if err := addProjectionDocument(s.idx, doc.TransactionId, doc.BlockId); err != nil {
		return err
	}

	return nil
}

func addProjectionDocument(projection *index.Index, txID string, blockID uint64) error {
	existing, err := projection.Get(txID)
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			if err := projection.Put(txID, blockID, 1, false); err != nil {
				return fmt.Errorf("shard: put index: %w", err)
			}
			return nil
		}
		return fmt.Errorf("shard: get index: %w", err)
	}

	if len(existing.BlockIDs) == 0 || blockID != existing.BlockIDs[len(existing.BlockIDs)-1] {
		if err := projection.AddBlockID(txID, blockID); err != nil {
			return fmt.Errorf("shard: add block id: %w", err)
		}
	}
	if err := projection.IncrementDocCount(txID); err != nil {
		return fmt.Errorf("shard: increment doc count: %w", err)
	}
	return nil
}

func (s *Shard) idxWriterForBlock(blockID uint64) *block.IndexWriter {
	if s.blockWriter != nil && s.blockWriter.BlockID() == blockID {
		return s.idxWriter
	}
	return nil
}

func (s *Shard) docExistsInPebble(txID, docName string) bool {
	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return false
	}
	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			continue
		}
		_, err = ir.Find(txID, docName)
		_ = ir.Close() // best-effort; data already read
		if err == nil {
			return true
		}
	}
	return false
}

func (s *Shard) writePrepFile(writeID string, entry *scrapv1.OpenlogEntry) error {
	data, err := proto.Marshal(entry)
	if err != nil {
		return err
	}
	path := s.prepPath(writeID)
	f, err := os.Create(path) //nolint:gosec // path constructed from known openlogDir + ULID
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close() // best-effort close after write failure
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() // best-effort close after sync failure
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(s.openlogDir)
}

func (s *Shard) prepPath(writeID string) string {
	return filepath.Join(s.openlogDir, writeID+".prep")
}

func (s *Shard) recoverOpenlog() error {
	entries, err := os.ReadDir(s.openlogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var prepFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".prep") {
			prepFiles = append(prepFiles, e.Name())
		}
	}
	sort.Strings(prepFiles)

	for _, name := range prepFiles {
		path := filepath.Join(s.openlogDir, name)
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from known openlogDir + directory listing
		if err != nil {
			return fmt.Errorf("shard: read prep %s: %w", name, err)
		}

		entry := &scrapv1.OpenlogEntry{}
		if err := proto.Unmarshal(data, entry); err != nil {
			return fmt.Errorf("shard: unmarshal prep %s: %w", name, err)
		}

		if s.docExistsInPebble(entry.TransactionId, entry.DocumentName) {
			_ = os.Remove(path) // best-effort cleanup of already-committed prep
			continue
		}

		blkPath := s.blockPath(entry.BlockId)
		if err := truncateFile(blkPath, safeUint64ToInt64(entry.StartOffset)); err != nil {
			return fmt.Errorf("shard: truncate block %d: %w", entry.BlockId, err)
		}
		_ = os.Remove(path) // best-effort cleanup after truncation
	}

	return nil
}

type docWithBlock struct {
	block.IndexEntry
	blockID uint64
}

func (s *Shard) findDocEntry(txID, docName string) (docWithBlock, error) {
	idxEntry, err := s.idx.Get(txID)
	if err != nil {
		return docWithBlock{}, fmt.Errorf("%w: %s", storeapi.ErrTxNotFound, txID)
	}

	for _, bid := range idxEntry.BlockIDs {
		idxPath := s.idxPath(bid)
		ir, err := block.OpenIndexReader(idxPath)
		if err != nil {
			return docWithBlock{}, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
		}
		entry, err := ir.Find(txID, docName)
		_ = ir.Close() // best-effort; data already read
		if err == nil {
			return docWithBlock{IndexEntry: entry, blockID: bid}, nil
		}
	}

	return docWithBlock{}, fmt.Errorf("%w: %s/%s", storeapi.ErrNotFound, txID, docName)
}

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

func truncateFile(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644) //nolint:gosec // path constructed from known blocksDir + block ID
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	truncErr := f.Truncate(size)
	if closeErr := f.Close(); closeErr != nil && truncErr == nil {
		return closeErr
	}
	return truncErr
}

func syncDir(path string) error {
	f, err := os.Open(path) //nolint:gosec // path is the shard's own openlogDir
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	if closeErr := f.Close(); closeErr != nil && syncErr == nil {
		return closeErr
	}
	return syncErr
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

func (s *Shard) CorruptProjectionForTest(txID string, blockID uint64, docCount uint16, completed bool) {
	_ = s.idx.Put(txID, blockID, docCount, completed)
}

type noopTransport struct{}

func (t *noopTransport) Send(_ []raftpb.Message) {}

// Ensure Shard satisfies store.Store and scrub interfaces at compile time.
var (
	_ storeapi.Store      = (*Shard)(nil)
	_ scrub.ResultCache   = (*Shard)(nil)
	_ scrub.Proposer      = (*Shard)(nil)
	_ scrub.LeaderChecker = (*Shard)(nil)
)

// Suppress unused import.
var _ = bytes.NewReader
