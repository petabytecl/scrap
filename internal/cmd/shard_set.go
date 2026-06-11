package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/peer"
	"github.com/petabytecl/scrap/internal/routing"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
)

const multiShardTelemetryLabel = "multi"

type shardSet struct {
	ids       []uint64
	shards    map[uint64]*shard.Shard
	closers   map[uint64]func() error
	dataDirs  map[uint64]string
	blockDirs map[uint64]string
}

type replicationTarget interface {
	AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error)
}

type raftTarget interface {
	RaftStep(ctx context.Context, msg raftpb.Message) error
}

type shardSetReplicationSink struct {
	shards interface {
		replicationTarget(shardID uint64) (replicationTarget, bool)
	}
}

type shardSetRaftRouter struct {
	shards interface {
		raftTarget(shardID uint64) (raftTarget, bool)
	}
}

type shardSetOpenConfig struct {
	cfg              Config
	topology         startupTopology
	raftID           uint64
	peers            map[uint64]string
	clientAddrs      map[uint64]string
	transport        *peer.SharedTransport
	peerClient       *peer.Client
	upload           shard.UploadConfig
	telemetryRuntime *scrapdTelemetryRuntime
	logger           *slog.Logger
	identifierMode   telemetry.IdentifierMode
	encryption       shard.EncryptionConfig
}

type openedLocalShard struct {
	shard *shard.Shard
	close func() error
}

type localShardOpener func(shardSetOpenConfig, uint64, string) (openedLocalShard, error)

type startupShardStatus struct {
	ShardID    uint64   `json:"shard_id"`
	Membership string   `json:"membership"`
	Routes     []string `json:"routes"`
	State      string   `json:"state"`
}

func openShardSet(openCfg shardSetOpenConfig) (*shardSet, error) {
	return openShardSetWithOpener(openCfg, openLocalShard)
}

func openShardSetWithOpener(openCfg shardSetOpenConfig, openShard localShardOpener) (*shardSet, error) {
	dataDirs, err := localShardDataDirs(openCfg.cfg.DataDir, openCfg.topology)
	if err != nil {
		return nil, err
	}

	set := &shardSet{
		ids:       append([]uint64(nil), openCfg.topology.LocalShardIDs...),
		shards:    make(map[uint64]*shard.Shard, len(openCfg.topology.LocalShardIDs)),
		closers:   make(map[uint64]func() error, len(openCfg.topology.LocalShardIDs)),
		dataDirs:  dataDirs,
		blockDirs: make(map[uint64]string, len(openCfg.topology.LocalShardIDs)),
	}
	for _, shardID := range set.ids {
		localShard, err := openShard(openCfg, shardID, dataDirs[shardID])
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("open Shard %d: %w", shardID, err)
		}
		set.shards[shardID] = localShard.shard
		set.closers[shardID] = localShard.close
		set.blockDirs[shardID] = filepath.Join(dataDirs[shardID], "blocks")
	}
	return set, nil
}

func openLocalShard(openCfg shardSetOpenConfig, shardID uint64, dataDir string) (openedLocalShard, error) {
	shardTel, err := openCfg.telemetryRuntime.newShardTelemetry()
	if err != nil {
		return openedLocalShard{}, fmt.Errorf("create Shard telemetry: %w", err)
	}

	uploadCfg := openCfg.upload
	uploadCfg.Metrics = shardTel.uploadMetrics

	localShard, err := shard.Open(shard.Config{
		DataDir:            dataDir,
		ShardID:            shardID,
		RaftID:             openCfg.raftID,
		Peers:              openCfg.peers,
		ClientAddrs:        openCfg.clientAddrs,
		BlockSealSize:      openCfg.cfg.BlockSealSize,
		Scrub:              openCfg.cfg.Scrub,
		Transport:          openCfg.transport.ForShard(shardID, openCfg.peers),
		Logger:             openCfg.logger,
		ConsistencyChecker: peer.NewClientConsistencyChecker(openCfg.peerClient),
		Metrics:            shardTel.scrubMetrics,
		DeepMetrics:        shardTel.deepScrubMetrics,
		Rebuilder:          peer.NewClientRebuilder(openCfg.peerClient),
		BlockTransferer:    openCfg.peerClient,
		Replicator:         openCfg.peerClient,
		PeerAddrs:          peerAddrsExceptSelf(openCfg.peers, openCfg.raftID),
		Upload:             uploadCfg,
		Eviction:           openCfg.cfg.Eviction,
		EvictionMetrics:    shardTel.evictionMetrics,
		MemberHostname:     openCfg.telemetryRuntime.resourceConfig.MemberSlotID,
		MemberID:           openCfg.telemetryRuntime.resourceConfig.MemberID,
		WriteTelemetry:     shardTel.writeTelemetry,
		IdentifierMode:     openCfg.identifierMode,
		Encryption:         openCfg.encryption,
	})
	if err != nil {
		return openedLocalShard{}, err
	}
	if err := openCfg.telemetryRuntime.registerRaftMetrics(localShard); err != nil {
		_ = localShard.Close()
		return openedLocalShard{}, fmt.Errorf("register raft metrics: %w", err)
	}
	if err := openCfg.telemetryRuntime.registerDiskMetrics(localShard); err != nil {
		_ = localShard.Close()
		return openedLocalShard{}, fmt.Errorf("register disk metrics: %w", err)
	}
	return openedLocalShard{shard: localShard, close: localShard.Close}, nil
}

func localShardDataDirs(root string, topology startupTopology) (map[uint64]string, error) {
	dirs := make(map[uint64]string, len(topology.LocalShardIDs))
	seen := make(map[string]uint64, len(topology.LocalShardIDs))
	for _, shardID := range topology.LocalShardIDs {
		dir := shardDataDir(root, topology, shardID)
		cleanDir := filepath.Clean(dir)
		if other, ok := seen[cleanDir]; ok {
			return nil, placementConfigError("local_shards %d and %d derive the same Shard data directory", other, shardID)
		}
		seen[cleanDir] = shardID
		dirs[shardID] = dir
	}
	return dirs, nil
}

func shardDataDir(root string, topology startupTopology, shardID uint64) string {
	if topology.SingleShardFallback {
		return root
	}
	return filepath.Join(root, "shards", "shard-"+strconv.FormatUint(shardID, 10))
}

func (s *shardSet) IDs() []uint64 {
	if s == nil {
		return nil
	}
	return append([]uint64(nil), s.ids...)
}

func (s *shardSet) DataDir(shardID uint64) (string, bool) {
	if s == nil {
		return "", false
	}
	dir, ok := s.dataDirs[shardID]
	return dir, ok
}

func (s *shardSet) BlockDirForShard(shardID uint64) (string, bool) {
	if s == nil {
		return "", false
	}
	dir, ok := s.blockDirs[shardID]
	return dir, ok
}

func (s *shardSet) singleShardStore() storeapi.Store {
	localShard, ok := s.singleShard()
	if !ok {
		return nil
	}
	return localShard
}

func (s *shardSet) singleShard() (*shard.Shard, bool) {
	if s == nil || len(s.ids) != 1 {
		return nil, false
	}
	localShard, ok := s.shards[s.ids[0]]
	return localShard, ok
}

func (s *shardSet) CheckReadiness(ctx context.Context) error {
	if s == nil || len(s.ids) == 0 {
		return status.Error(codes.FailedPrecondition, "no local Shards configured")
	}
	for _, shardID := range s.ids {
		if err := s.shards[shardID].CheckReadiness(ctx); err != nil {
			return fmt.Errorf("shard %d readiness: %w", shardID, err)
		}
	}
	return nil
}

func (s *shardSet) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	for i := len(s.ids) - 1; i >= 0; i-- {
		shardID := s.ids[i]
		closeShard := s.closers[shardID]
		if closeShard == nil && s.shards[shardID] != nil {
			closeShard = s.shards[shardID].Close
		}
		if closeShard == nil {
			continue
		}
		if err := closeShard(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *shardSet) replicationTarget(shardID uint64) (replicationTarget, bool) {
	localShard, ok := s.shards[shardID]
	return localShard, ok
}

func (s *shardSet) raftTarget(shardID uint64) (raftTarget, bool) {
	localShard, ok := s.shards[shardID]
	return localShard, ok
}

func (d shardSetReplicationSink) AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error) {
	if d.shards == nil {
		return nil, status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	target, ok := d.shards.replicationTarget(init.GetShardId())
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	return target.AppendReplicatedDocument(ctx, init, body)
}

func (d shardSetRaftRouter) RouteRaftMessage(ctx context.Context, shardID uint64, msg raftpb.Message) error {
	if d.shards == nil {
		return status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	target, ok := d.shards.raftTarget(shardID)
	if !ok {
		return status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	return target.RaftStep(ctx, msg)
}

func (s *shardSet) AppendReplicatedDocument(ctx context.Context, init *scrapv1.ReplicateDocumentInit, body io.Reader) ([]byte, error) {
	localShard, ok := s.shards[init.GetShardId()]
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	return localShard.AppendReplicatedDocument(ctx, init, body)
}

func (s *shardSet) RouteRaftMessage(ctx context.Context, shardID uint64, msg raftpb.Message) error {
	localShard, ok := s.shards[shardID]
	if !ok {
		return status.Error(codes.FailedPrecondition, "local Shard not configured")
	}
	return localShard.RaftStep(ctx, msg)
}

func (s *shardSet) StartupStatus(topology startupTopology) []startupShardStatus {
	byShard := routeRangesByShard(topology.Placement.RouteMapSummary())
	statuses := make([]startupShardStatus, 0, len(byShard))
	shardIDs := make([]uint64, 0, len(byShard))
	for shardID := range byShard {
		shardIDs = append(shardIDs, shardID)
	}
	sort.Slice(shardIDs, func(i, j int) bool {
		return shardIDs[i] < shardIDs[j]
	})
	local := shardIDSet(s.IDs())
	for _, shardID := range shardIDs {
		membership := "remote"
		state := "not_local"
		if _, ok := local[shardID]; ok {
			membership = "local"
			state = "open"
		}
		statuses = append(statuses, startupShardStatus{
			ShardID:    shardID,
			Membership: membership,
			Routes:     byShard[shardID],
			State:      state,
		})
	}
	return statuses
}

func routeRangesByShard(ranges []routing.SlotRange) map[uint64][]string {
	byShard := make(map[uint64][]string)
	for _, r := range ranges {
		byShard[r.ShardID] = append(byShard[r.ShardID], fmt.Sprintf("%d-%d", r.StartSlot, r.EndSlot))
	}
	return byShard
}

func shardIDSet(ids []uint64) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

type failClosedPublicStore struct{}

func (failClosedPublicStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, multiShardPublicRoutingPending()
}

func (failClosedPublicStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, multiShardPublicRoutingPending()
}

func (failClosedPublicStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, multiShardPublicRoutingPending()
}

func (failClosedPublicStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, multiShardPublicRoutingPending()
}

func multiShardPublicRoutingPending() error {
	return storeapi.NewUnavailable(
		storeapi.UnavailableReasonShardRoutingPending,
		"multi-Shard public routing is not configured",
	)
}

func topologyTelemetryShardLabel(topology startupTopology) string {
	if topology.SingleShardFallback && len(topology.LocalShardIDs) == 1 {
		return ""
	}
	return multiShardTelemetryLabel
}

func publicStoreForTopology(set *shardSet, topology startupTopology) storeapi.Store {
	if topology.SingleShardFallback {
		return set.singleShardStore()
	}
	return failClosedPublicStore{}
}
