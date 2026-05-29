package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	raftpb "go.etcd.io/raft/v3/raftpb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/logbridge"
	"github.com/petabytecl/scrap/internal/peer"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	"github.com/petabytecl/scrap/internal/telemetry"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "scrapd: %v\n", err)
		os.Exit(1)
	}
}

const (
	shutdownTimeout = 5 * time.Second
	defaultPeerPort = 9091
)

//nolint:cyclop // main startup function wiring all subsystems
func run() error {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		return err
	}

	logger := logbridge.NewLoggerFromEnv(os.Stderr)

	uploadBackend, backendType, err := openConfiguredUploadBackend(context.Background(), cfg.DataDir, cfg.UploadEnabled)
	if err != nil {
		return err
	}

	// Telemetry identifiers are hashed by default; raw Document identifiers are
	// emitted only in the reserved local non-production Cell, and the request is
	// refused (fail-closed) anywhere else. See ADR 0013 §4.
	identifierMode := telemetry.ResolveIdentifierMode(cfg.CellID, cfg.RawTelemetryIDs)
	switch {
	case identifierMode == telemetry.RawIdentifiersForLocalDebug:
		logger.Warn("telemetry raw identifiers ENABLED for local debugging (local non-production Cell only)", "cell_id", cfg.CellID)
	case cfg.RawTelemetryIDs:
		logger.Warn("SCRAP_TELEMETRY_RAW_IDS ignored: raw telemetry identifiers are permitted only in the local non-production Cell", "cell_id", cfg.CellID)
	}

	peers, raftID, err := resolvePeers(cfg)
	if err != nil {
		return err
	}

	telemetryRuntime, err := newScrapdTelemetryForHost(context.Background(), cfg.DataDir, raftID, 0)
	if err != nil {
		return err
	}
	defer func() { _ = telemetryRuntime.Shutdown(context.Background()) }()

	shardTel, err := telemetryRuntime.newShardTelemetry()
	if err != nil {
		return fmt.Errorf("create shard telemetry: %w", err)
	}

	uploadCfg := shard.UploadConfig{
		Enabled:     cfg.UploadEnabled,
		Backend:     uploadBackend,
		CellID:      cfg.CellID,
		Concurrency: cfg.UploadConcurrency,
		Pressure:    cfg.UploadPressure,
		Metrics:     shardTel.uploadMetrics,
	}

	sharedTransport := peer.NewSharedTransport(peers)
	defer sharedTransport.Close()
	shardTransport := sharedTransport.ForShard(0, peers)

	peerClient := peer.NewClient()

	peerAddrs := peerAddrsExceptSelf(peers, raftID)

	s, err := shard.Open(shard.Config{
		DataDir:            cfg.DataDir,
		ShardID:            0,
		RaftID:             raftID,
		Peers:              peers,
		BlockSealSize:      cfg.BlockSealSize,
		Scrub:              cfg.Scrub,
		Transport:          shardTransport,
		Logger:             logger,
		ConsistencyChecker: peer.NewClientConsistencyChecker(peerClient),
		Metrics:            shardTel.scrubMetrics,
		DeepMetrics:        shardTel.deepScrubMetrics,
		Rebuilder:          peer.NewClientRebuilder(peerClient),
		BlockRepairer:      peer.NewClientBlockRepairer(peerClient, filepath.Join(cfg.DataDir, "blocks")),
		Replicator:         peerClient,
		PeerAddrs:          peerAddrs,
		Upload:             uploadCfg,
		WriteTelemetry:     shardTel.writeTelemetry,
		IdentifierMode:     identifierMode,
	})
	if err != nil {
		return fmt.Errorf("open shard: %w", err)
	}
	defer func() { _ = s.Close() }()
	defer peerClient.Close()

	if err := telemetryRuntime.registerRaftMetrics(s); err != nil {
		return fmt.Errorf("register raft metrics: %w", err)
	}
	if err := telemetryRuntime.registerDiskMetrics(s); err != nil {
		return fmt.Errorf("register disk metrics: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lc := net.ListenConfig{}

	clientLis, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen client %s: %w", cfg.ListenAddr, err)
	}

	gs := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	server.Register(gs, s, server.WithTelemetry(telemetryRuntime.server), server.WithIdentifierMode(identifierMode))
	server.RegisterHealth(gs, s)

	peerLis, err := lc.Listen(ctx, "tcp", cfg.PeerAddr)
	if err != nil {
		return fmt.Errorf("listen peer %s: %w", cfg.PeerAddr, err)
	}
	peerGS := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	peerSrv := peer.NewServer(cfg.DataDir+"/blocks", peer.WithScrubCache(s), peer.WithRebuildHandler(s), peer.WithReplicationSink(s))
	peerSrv.SetRaftRouter(peer.RaftRouterFunc(func(ctx context.Context, _ uint64, msg raftpb.Message) error {
		return s.RaftStep(ctx, msg)
	}))
	peer.RegisterServer(peerGS, peerSrv)

	adminOpts := []admin.Option{}
	adminOpts = append(adminOpts, admin.WithUploadPressureProvider(s))
	adminOpts = append(adminOpts, admin.WithMetrics(telemetryRuntime.metricsHandler))
	if cfg.TestHooks {
		adminOpts = append(adminOpts, admin.WithProjectionInjector(s))
	}
	if cfg.PprofEnabled {
		adminOpts = append(adminOpts, admin.WithPprof())
	}
	adminSrv := admin.New(adminOpts...)
	go func() { _ = adminSrv.ListenAndServe(cfg.AdminAddr) }()
	go func() { _ = peerGS.Serve(peerLis) }()
	go func() { _ = gs.Serve(clientLis) }()

	logger.Info("scrapd starting",
		"client_addr", cfg.ListenAddr,
		"peer_addr", cfg.PeerAddr,
		"admin_addr", cfg.AdminAddr,
		"data_dir", cfg.DataDir,
		"raft_id", raftID,
		"cell_id", cfg.CellID,
		"peers", len(peers),
		"scrub_enabled", cfg.Scrub.Enabled,
		"backend_type", backendType,
		"upload_enabled", uploadCfg.Enabled,
		"upload_concurrency", uploadCfg.Concurrency,
		"upload_budget_bytes", uploadCfg.Pressure.BudgetBytes,
	)

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("admin shutdown failed", "err", err)
	}
	peerGS.GracefulStop()
	// peerGS has stopped accepting RPCs, so no new peer writers can be created;
	// flush and close any block/index writers the peer server still owns.
	if err := peerSrv.Close(); err != nil {
		logger.Error("peer writer close failed", "err", err)
	}
	gs.GracefulStop()

	return nil
}

func openConfiguredUploadBackend(ctx context.Context, dataDir string, enabled bool) (backend.Backend, string, error) {
	if !enabled {
		return nil, "", nil
	}
	return openUploadBackend(ctx, dataDir)
}

func openUploadBackend(ctx context.Context, dataDir string) (backend.Backend, string, error) {
	backendType := os.Getenv("SCRAP_BACKEND_TYPE")
	if backendType == "" {
		backendType = "fs"
	}

	switch backendType {
	case "fs":
		return backend.NewFS(filepath.Join(dataDir, "backend")), backendType, nil
	case "s3":
		cfg, err := backend.ParseS3ConfigFromEnv()
		if err != nil {
			return nil, "", fmt.Errorf("parse S3 backend config: %w", err)
		}
		store, err := backend.NewS3FromConfig(ctx, cfg)
		if err != nil {
			return nil, "", fmt.Errorf("open S3 backend: %w", err)
		}
		return store, backendType, nil
	default:
		return nil, "", fmt.Errorf("unsupported SCRAP_BACKEND_TYPE %q", backendType)
	}
}

func resolvePeers(cfg Config) (map[uint64]string, uint64, error) {
	switch {
	case cfg.PeersFlag != "":
		peers, err := scrapraft.ParsePeersFlag(cfg.PeersFlag)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid --peers: %w", err)
		}
		hostname, _ := os.Hostname()
		ord, oErr := scrapraft.ParseOrdinal(hostname)
		if oErr != nil {
			return peers, 1, nil //nolint:nilerr // ordinal parse failure is expected for non-StatefulSet hostnames; fall back to raft ID 1
		}
		return peers, scrapraft.OrdinalToRaftID(ord), nil

	case cfg.Replicas > 0 && cfg.HeadlessService != "":
		hostname, err := os.Hostname()
		if err != nil {
			return nil, 0, fmt.Errorf("hostname: %w", err)
		}
		ord, err := scrapraft.ParseOrdinal(hostname)
		if err != nil {
			return nil, 0, fmt.Errorf("parse ordinal from %q: %w", hostname, err)
		}
		raftID := scrapraft.OrdinalToRaftID(ord)
		peers := scrapraft.BuildK8sPeers(cfg.Replicas, cfg.HeadlessService, cfg.Namespace, cfg.PeerPort)
		return peers, raftID, nil

	default:
		return map[uint64]string{1: "localhost:9091"}, 1, nil
	}
}

func peerAddrsExceptSelf(peers map[uint64]string, selfID uint64) []string {
	addrs := make([]string, 0, len(peers))
	for id, addr := range peers {
		if id == selfID {
			continue
		}
		addrs = append(addrs, addr)
	}
	return addrs
}
