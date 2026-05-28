package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/logbridge"
	"github.com/petabytecl/scrap/internal/peer"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
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

func run() error {
	dataDir := flag.String("data-dir", "/data", "storage root directory")
	listenAddr := flag.String("listen-addr", ":9090", "gRPC client listen address")
	peerAddr := flag.String("peer-addr", ":9091", "gRPC peer listen address")
	adminAddr := flag.String("admin-addr", ":8080", "HTTP admin listen address (metrics)")
	blockSealSize := flag.Int64("block-seal-size", shard.DefaultBlockSealSize, "block seal threshold in bytes")
	peersFlag := flag.String("peers", "", "raft peers (e.g. 1=localhost:9091,2=localhost:9092)")
	flag.Parse()

	logger := logbridge.NewLoggerFromEnv(os.Stderr)

	scrubCfg := scrub.ParseScrubConfig()
	registry := prometheus.NewRegistry()
	uploadMetrics := shard.NewUploadPrometheusMetrics(registry)
	uploadEnabled := envBool("SCRAP_UPLOAD_ENABLED", true)

	uploadBackend, backendType, err := openConfiguredUploadBackend(context.Background(), *dataDir, uploadEnabled)
	if err != nil {
		return err
	}

	cellID := os.Getenv("SCRAP_CELL_ID")
	uploadCfg := shard.UploadConfig{
		Enabled:     uploadEnabled,
		Backend:     uploadBackend,
		CellID:      cellID,
		Concurrency: envInt("SCRAP_UPLOAD_CONCURRENCY", shard.DefaultUploadConcurrency),
		Pressure:    shard.ParseUploadPressureConfigFromEnv(),
		Metrics:     uploadMetrics,
	}

	peers, raftID, err := resolvePeers(*peersFlag)
	if err != nil {
		return err
	}

	telemetryRuntime, err := newScrapdTelemetryForHost(context.Background(), *dataDir, raftID, 0)
	if err != nil {
		return err
	}
	defer func() { _ = telemetryRuntime.Shutdown(context.Background()) }()

	sharedTransport := peer.NewSharedTransport(peers)
	defer sharedTransport.Close()
	shardTransport := sharedTransport.ForShard(0, peers)

	peerClient := peer.NewClient()

	peerAddrs := peerAddrsExceptSelf(peers, raftID)

	scrubMetrics := scrub.NewPrometheusMetrics(registry)
	deepScrubMetrics := scrub.NewDeepScrubPrometheusMetrics(registry)

	s, err := shard.Open(shard.Config{
		DataDir:            *dataDir,
		ShardID:            0,
		RaftID:             raftID,
		Peers:              peers,
		BlockSealSize:      *blockSealSize,
		Scrub:              scrubCfg,
		Transport:          shardTransport,
		Logger:             logger,
		ConsistencyChecker: peer.NewClientConsistencyChecker(peerClient),
		ScrubMetrics:       scrubMetrics,
		DeepScrubMetrics:   deepScrubMetrics,
		Rebuilder:          peer.NewClientRebuilder(peerClient),
		BlockRepairer:      peer.NewClientBlockRepairer(peerClient, filepath.Join(*dataDir, "blocks")),
		Replicator:         peerClient,
		PeerAddrs:          peerAddrs,
		Upload:             uploadCfg,
	})
	if err != nil {
		return fmt.Errorf("open shard: %w", err)
	}
	defer func() { _ = s.Close() }()
	defer peerClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lc := net.ListenConfig{}

	clientLis, err := lc.Listen(ctx, "tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen client %s: %w", *listenAddr, err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s, server.WithTelemetry(telemetryRuntime.server))
	server.RegisterHealth(gs, s)

	peerLis, err := lc.Listen(ctx, "tcp", *peerAddr)
	if err != nil {
		return fmt.Errorf("listen peer %s: %w", *peerAddr, err)
	}
	peerGS := grpc.NewServer()
	peerSrv := peer.NewServer(*dataDir+"/blocks", peer.WithScrubCache(s), peer.WithRebuildHandler(s), peer.WithReplicationSink(s))
	peerSrv.SetRaftRouter(peer.RaftRouterFunc(func(ctx context.Context, _ uint64, msg raftpb.Message) error {
		return s.RaftStep(ctx, msg)
	}))
	peer.RegisterServer(peerGS, peerSrv)

	adminOpts := []admin.Option{}
	adminOpts = append(adminOpts, admin.WithUploadPressureProvider(s))
	if envBool("SCRAP_TEST_HOOKS", false) {
		adminOpts = append(adminOpts, admin.WithProjectionInjector(s))
	}
	adminSrv := admin.New(registry, adminOpts...)
	go func() { _ = adminSrv.ListenAndServe(*adminAddr) }()
	go func() { _ = peerGS.Serve(peerLis) }()
	go func() { _ = gs.Serve(clientLis) }()

	logger.Info("scrapd starting",
		"client_addr", *listenAddr,
		"peer_addr", *peerAddr,
		"admin_addr", *adminAddr,
		"data_dir", *dataDir,
		"raft_id", raftID,
		"cell_id", cellID,
		"peers", len(peers),
		"scrub_enabled", scrubCfg.Enabled,
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

func resolvePeers(peersFlag string) (map[uint64]string, uint64, error) {
	replicas := envInt("SCRAP_REPLICAS", 0)
	headlessSvc := os.Getenv("SCRAP_HEADLESS_SERVICE")
	peerPort := envInt("SCRAP_PEER_PORT", defaultPeerPort)
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	switch {
	case peersFlag != "":
		peers, err := scrapraft.ParsePeersFlag(peersFlag)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid --peers: %w", err)
		}
		hostname, _ := os.Hostname()
		ord, oErr := scrapraft.ParseOrdinal(hostname)
		if oErr != nil {
			return peers, 1, nil //nolint:nilerr // ordinal parse failure is expected for non-StatefulSet hostnames; fall back to raft ID 1
		}
		return peers, scrapraft.OrdinalToRaftID(ord), nil

	case replicas > 0 && headlessSvc != "":
		hostname, err := os.Hostname()
		if err != nil {
			return nil, 0, fmt.Errorf("hostname: %w", err)
		}
		ord, err := scrapraft.ParseOrdinal(hostname)
		if err != nil {
			return nil, 0, fmt.Errorf("parse ordinal from %q: %w", hostname, err)
		}
		raftID := scrapraft.OrdinalToRaftID(ord)
		peers := scrapraft.BuildK8sPeers(replicas, headlessSvc, namespace, peerPort)
		return peers, raftID, nil

	default:
		return map[uint64]string{1: "localhost:9091"}, 1, nil
	}
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "TRUE", "True":
		return true
	case "0", "false", "FALSE", "False":
		return false
	default:
		return fallback
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
