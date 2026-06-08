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
	"sync"
	"syscall"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/localblock"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/scrub"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
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
	ClientAddrs    map[uint64]string
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
	BlockTransferer    scrub.BlockTransferer
	LatencySignal      scrub.LatencySignal
	Replicator         DocumentReplicator
	PeerAddrs          []string
	Upload             UploadConfig
	Eviction           EvictionConfig
	EvictionMetrics    EvictionMetrics
	MemberHostname     string
	MemberID           string
	WriteTelemetry     WriteStageRecorder
	IdentifierMode     telemetry.IdentifierMode
	Encryption         EncryptionConfig
}

type EncryptionConfig struct {
	Transit      encryption.Transit
	TransitMount string
	TransitKey   string
}

type raftNode interface {
	Propose(ctx context.Context, data []byte) error
	ReadIndex(ctx context.Context) (uint64, error)
	Step(ctx context.Context, msg raftpb.Message) error
	IsLeader() bool
	LeaderID() uint64
	AppliedIndex() uint64
	CommitIndex() uint64
	WithStableLeadership(func() error) error
	Stop()
}

type Shard struct {
	dataDir         string
	blocksDir       string
	openlogDir      string
	shardID         uint64
	raftID          uint64
	peers           map[uint64]string
	clientAddrs     map[uint64]string
	idx             *index.Index
	raft            raftNode
	replicator      DocumentReplicator
	upload          UploadConfig
	eviction        EvictionConfig
	evictionMetrics EvictionMetrics
	memberHostname  string
	memberID        string
	writeTelemetry  WriteStageRecorder
	identifierMode  telemetry.IdentifierMode
	encryption      EncryptionConfig
	baseLogger      *slog.Logger
	logger          *slog.Logger
	blockSealSize   int64
	raftStartedAt   time.Time
	bootstrapGrace  time.Duration

	mu          sync.Mutex
	blockWriter *block.Writer
	idxWriter   *block.IndexWriter
	nextBlockID uint64

	// Serializes the leader write pipeline so peer replication and Raft proposals
	// preserve the same offset order as local block appends.
	writeOrderMu sync.Mutex

	proposalMu sync.Mutex
	proposals  map[string]chan error

	scrubs                  *scrubCoordinator
	uploads                 *uploadController
	uploadPressureScrubGate *pressurePauseGate
	blockUploads            *blockUploadLifecycle

	rebuilder *projectionRebuilder

	lifecycleCleanupDone   chan struct{}
	lifecycleMutationMu    sync.Mutex
	evictionCampaigns      *eviction.Campaigns
	restoreFailuresByBlock map[uint64]string

	evictionHealthMu       sync.Mutex
	evictionHealthSnapshot eviction.HealthSnapshot
	evictionHealthBlocks   map[uint64]evictionHealthBlockContribution

	restoreMu sync.Mutex
	restores  map[uint64]*blockRestoreCall
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
	if c.EvictionMetrics == nil {
		c.EvictionMetrics = noopEvictionMetrics{}
	}
	if c.MemberHostname == "" {
		c.MemberHostname = "local"
	}
	if c.MemberID == "" {
		c.MemberID = "local"
	}
	c.Eviction = c.Eviction.withDefaults()
	c.Encryption = c.Encryption.withDefaults()
}

func (c EncryptionConfig) withDefaults() EncryptionConfig {
	if c.Transit == nil {
		return c
	}
	if c.TransitMount == "" {
		c.TransitMount = encryption.DefaultTransitMountPath
	}
	if c.TransitKey == "" {
		c.TransitKey = encryption.DefaultTransitKeyName
	}
	return c
}

func (c EncryptionConfig) enabled() bool {
	return c.Transit != nil
}

func (c EncryptionConfig) documentConfig() encryption.DocumentConfig {
	return encryption.DocumentConfig{
		Transit:      c.Transit,
		TransitMount: c.TransitMount,
		TransitKey:   c.TransitKey,
	}
}

func Open(cfg Config) (*Shard, error) {
	cfg.applyDefaults()
	if err := cfg.Eviction.Validate(); err != nil {
		return nil, fmt.Errorf("shard: eviction config: %w", err)
	}

	baseLogger := openLogger(cfg.Logger)
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
		clientAddrs:             cfg.ClientAddrs,
		idx:                     idx,
		baseLogger:              baseLogger,
		logger:                  logger,
		replicator:              cfg.Replicator,
		upload:                  cfg.Upload,
		eviction:                cfg.Eviction,
		evictionMetrics:         cfg.EvictionMetrics,
		memberHostname:          cfg.MemberHostname,
		memberID:                cfg.MemberID,
		writeTelemetry:          cfg.WriteTelemetry,
		identifierMode:          cfg.IdentifierMode,
		encryption:              cfg.Encryption,
		blockSealSize:           cfg.BlockSealSize,
		nextBlockID:             nextID,
		proposals:               make(map[string]chan error),
		evictionCampaigns:       eviction.NewCampaigns(),
		evictionHealthBlocks:    make(map[uint64]evictionHealthBlockContribution),
		blockUploads:            newBlockUploadLifecycle(),
		restores:                make(map[uint64]*blockRestoreCall),
		uploadPressureScrubGate: newPressurePauseGate(),
		raftStartedAt:           time.Now(),
		bootstrapGrace:          cfg.BootstrapGrace,
	}
	s.scrubs = newScrubCoordinator(s, blocksDir, baseLogger, s.uploadPressureScrubGate)
	s.rebuilder = newProjectionRebuilder(s, cfg.DataDir, blocksDir, cfg.ShardID, cfg.Upload, logger)
	// Raft Open starts its run loop before returning and can replay committed upload
	// commands immediately. The apply path refreshes upload pressure, so the
	// controller must exist before Raft replay begins. Start still waits until after
	// s.raft is assigned below.
	s.uploads = newUploadController(s, cfg.Upload, s.shardID, s.logger, s.writeTelemetry, s.uploadPressureScrubGate)
	s.uploads.resetConcurrency()

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

	if err := s.refreshRuntimeStateAfterRaftOpen(); err != nil {
		raftNode.Stop()
		s.closeBlockAndIdx()
		_ = idx.Close()
		return nil, err
	}
	s.uploads.setAuthPausedMetric(false)

	s.scrubs.Start(cfg)
	s.uploads.Start()
	s.startLifecycleCleanup()

	return s, nil
}

func (s *Shard) refreshRuntimeStateAfterRaftOpen() error {
	s.mu.Lock()
	refreshErr := s.refreshUploadPressureLocked()
	s.mu.Unlock()
	if refreshErr != nil {
		return fmt.Errorf("shard: refresh upload pressure: %w", refreshErr)
	}
	if err := s.rebuildEvictionHealthSnapshot(context.Background()); err != nil {
		return fmt.Errorf("shard: rebuild eviction health: %w", err)
	}
	return nil
}

func openLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

func ensureShardDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("shard: mkdir %s: %w", dir, err)
		}
	}
	return nil
}

//nolint:cyclop,gocognit // orchestration function managing seal check, block append, peer replication, raft propose, and apply
func (s *Shard) WriteDocument(ctx context.Context, txID, docName, contentType, idempotencyKey string, body io.Reader) (storeapi.WriteResult, error) {
	if err := s.requireLeader(); err != nil {
		return storeapi.WriteResult{}, err
	}

	s.writeOrderMu.Lock()
	defer s.writeOrderMu.Unlock()

	if err := s.requireWritableLeader(); err != nil {
		return storeapi.WriteResult{}, err
	}

	s.mu.Lock()

	key := txID + "\x00" + docName
	exists, err := s.documentVisibleInProjection(txID, docName)
	if err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, err
	}
	if exists {
		s.mu.Unlock()
		return storeapi.WriteResult{}, fmt.Errorf("%w: %s/%s", storeapi.ErrAlreadyExists, txID, docName)
	}

	if s.blockWriter.Offset() > block.HeaderSize && s.blockWriter.Offset() >= s.blockSealSize {
		if err := s.sealAndOpenNew(ctx); err != nil {
			s.mu.Unlock()
			return storeapi.WriteResult{}, fmt.Errorf("shard: seal block: %w", err)
		}
	}

	blockID := s.blockWriter.BlockID()
	startOffset := s.blockWriter.Offset()

	attempt, err := s.beginOpenlogWriteAttempt(openlogWriteAttemptConfig{
		txID:           txID,
		docName:        docName,
		contentType:    contentType,
		idempotencyKey: idempotencyKey,
		blockID:        blockID,
		startOffset:    startOffset,
	})
	if err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, err
	}
	defer attempt.cleanupOnAbort()

	now := time.Now()

	ctx, appendStage := s.writeTelemetry.StartStage(ctx, "block_append")
	result, replicationData, envelope, err := s.appendDocumentPayload(ctx, txID, docName, contentType, body)
	appendStage.End(err)
	if err != nil {
		s.mu.Unlock()
		return storeapi.WriteResult{}, mapEncryptionError(fmt.Errorf("shard: append document: %w", err))
	}

	s.mu.Unlock()

	ctx, replicateStage := s.writeTelemetry.StartStage(ctx, "peer_replicate")
	err = s.replicateDocument(ctx, attempt.prepEntry(), contentType, result, replicationData, envelope)
	replicateStage.End(err)
	if err != nil {
		return storeapi.WriteResult{}, err
	}

	doneCh := make(chan error, 1)
	s.proposalMu.Lock()
	s.proposals[key] = doneCh
	s.proposalMu.Unlock()

	ctx, proposeStage := s.writeTelemetry.StartStage(ctx, "raft_propose")
	// Stamp the active propose span into the command so every voter's apply loop
	// recovers it and emits a child apply span on all replicas. See ADR 0013.
	cmd := attempt.commitCommand(result, now, envelope)
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

	attempt.complete()

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
	if err := s.ensureMetadataReadAllowed(entry.blockID); err != nil {
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

	rc, meta, err := s.readDocumentFromProjection(ctx, txID, docName, localblock.RestoreReasonRead, 0)
	return rc, meta, err
}

func (s *Shard) readDocumentFromProjection(
	ctx context.Context,
	txID string,
	docName string,
	restoreReason string,
	expectedBlockID uint64,
) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.mu.Lock()

	entry, err := s.findDocEntry(txID, docName)
	if err != nil {
		s.mu.Unlock()
		return nil, storeapi.DocumentMeta{}, err
	}
	if expectedBlockID != 0 && entry.blockID != expectedBlockID {
		s.mu.Unlock()
		return nil, storeapi.DocumentMeta{}, fmt.Errorf(
			"%w: projection resolved Block %d for %s/%s, want Block %d",
			storeapi.ErrDataLoss,
			entry.blockID,
			txID,
			docName,
			expectedBlockID,
		)
	}
	if err := s.ensureReadableBlockLockedForReason(ctx, entry.blockID, restoreReason); err != nil {
		return nil, storeapi.DocumentMeta{}, err
	}
	defer s.mu.Unlock()

	blkPath := s.blockPath(entry.blockID)
	rc, err := s.readDocumentBytes(ctx, blkPath, entry.IndexEntry)
	if err != nil {
		return nil, storeapi.DocumentMeta{}, mapReadDocumentError(err)
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

func (s *Shard) readDocumentBytes(ctx context.Context, blkPath string, entry block.IndexEntry) (io.ReadCloser, error) {
	if len(entry.EncryptionEnvelope) == 0 {
		return block.ReadDocument(blkPath, entry)
	}
	if !s.encryption.enabled() {
		return nil, storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "key material unavailable")
	}
	frames, err := block.ReadDocumentFrames(blkPath, entry)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
	}
	plaintext, err := encryption.DecryptDocument(ctx, s.encryption.Transit, encryption.DocumentIdentity{
		TransactionID: entry.TransactionID,
		DocumentName:  entry.DocName,
	}, entry.EncryptionEnvelope, frames, entry.SHA256, entry.TotalBytes)
	if err != nil {
		return nil, mapEncryptionError(err)
	}
	return io.NopCloser(bytes.NewReader(plaintext)), nil
}

func mapReadDocumentError(err error) error {
	if errors.Is(err, storeapi.ErrUnavailable) || errors.Is(err, storeapi.ErrDataLoss) {
		return err
	}
	return fmt.Errorf("%w: %w", storeapi.ErrDataLoss, err)
}

func (s *Shard) FindDocuments(ctx context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	if err := s.requireLeaderRead(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	resolved, err := s.projectionResolver().ListDocuments(txID)
	if err != nil {
		return nil, mapProjectionResolutionError(txID, "", err)
	}
	if err := s.ensureMetadataReadsAllowed(resolved); err != nil {
		return nil, err
	}

	docs := make([]storeapi.DocumentMeta, 0, len(resolved))
	for _, doc := range resolved {
		docs = append(docs, storeapi.DocumentMeta{
			Name:        doc.DocName,
			ContentType: doc.ContentType,
			Size:        doc.TotalBytes,
			SHA256:      doc.SHA256,
			CreatedAt:   doc.CreatedAt,
		})
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
	if s.rebuilder.InProgress() {
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
	if s.rebuilder.InProgress() {
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
	return s.uploads.rejectWrite()
}

func (s *Shard) requireLeaderRead(ctx context.Context) error {
	if s.rebuilder.InProgress() {
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.evictionCampaigns.HasRunningApply() {
		return true, nil
	}
	return s.rebuilder.Trigger(ctx)
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

func (s *Shard) swapRebuiltProjection(pebbleDir, tempDir, oldDir string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.swapRebuiltProjectionLocked(pebbleDir, tempDir, oldDir)
	return s.idx == nil, err
}

func (s *Shard) confirmedUploadForRebuild(blockID uint64) (index.ConfirmedUpload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.committedConfirmUploadAuthorityLocked(blockID)
}

func (s *Shard) committedConfirmUploadAuthorityLocked(blockID uint64) (index.ConfirmedUpload, error) {
	confirmed, loadedFromDisk, err := s.blockUploadLifecycleLocked().confirmedUploadAuthority(s.blocksDir, blockID)
	if err != nil {
		return index.ConfirmedUpload{}, err
	}
	if loadedFromDisk {
		s.recordEvictionHealthBlockBestEffort(confirmed.BlockID)
	}
	return confirmed, nil
}

func (s *Shard) currentOpenBlockID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.blockWriter == nil {
		return 0
	}
	return s.blockWriter.BlockID()
}

func (s *Shard) WaitRebuild() {
	s.rebuilder.Wait()
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
	return s.blockUploadLifecycleLocked().obligationCount()
}

func (s *Shard) SetRebuildingForTest(v bool) {
	s.rebuilder.setInProgressForTest(v)
}

func (s *Shard) notLeaderError() error {
	leaderID := s.raft.LeaderID()
	return &storeapi.NotLeaderError{LeaderAddr: s.leaderClientAddr(leaderID)}
}

func (s *Shard) leaderClientAddr(leaderID uint64) string {
	if leaderID == 0 {
		return ""
	}
	if s.clientAddrs != nil {
		if addr, ok := s.clientAddrs[leaderID]; ok {
			return addr
		}
	}
	if addr, ok := s.peers[leaderID]; ok {
		return addr
	}
	return ""
}

func (s *Shard) RaftStep(ctx context.Context, msg raftpb.Message) error {
	return s.raft.Step(ctx, msg)
}

func (s *Shard) Close() error {
	s.scrubs.Stop()
	s.uploads.Stop()
	s.raft.Stop()
	s.WaitRebuild()
	s.waitLifecycleCleanup()

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

func (s *Shard) ProposeConsistencyCheck(ctx context.Context, scrubID string) (scrub.Result, error) {
	return s.scrubs.ProposeConsistencyCheck(ctx, scrubID)
}

func (s *Shard) GetScrubResult(scrubID string) (scrub.Result, bool) {
	return s.scrubs.GetScrubResult(scrubID)
}

type noopTransport struct{}

func (t *noopTransport) Send(_ []raftpb.Message) {}

// Ensure Shard satisfies store.Store and scrub interfaces at compile time.
var (
	_ storeapi.Store      = (*Shard)(nil)
	_ scrub.ResultCache   = (*Shard)(nil)
	_ scrub.LeaderChecker = (*Shard)(nil)
)

// Suppress unused import.
var _ = bytes.NewReader
