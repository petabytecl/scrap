package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	raftpb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/admin"
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

	cellID := os.Getenv("SCRAP_CELL_ID")

	peers, raftID, err := resolvePeers(*peersFlag)
	if err != nil {
		return err
	}

	sharedTransport := peer.NewSharedTransport(peers)
	defer sharedTransport.Close()
	shardTransport := sharedTransport.ForShard(0, peers)

	peerClient := peer.NewClient()

	var peerAddrs []string
	for _, addr := range peers {
		peerAddrs = append(peerAddrs, addr)
	}

	scrubMetrics := scrub.NewPrometheusMetrics(registry)

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
		Rebuilder:          peer.NewClientRebuilder(peerClient),
		PeerAddrs:          peerAddrs,
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
	server.Register(gs, s)
	server.RegisterHealth(gs, s)

	peerLis, err := lc.Listen(ctx, "tcp", *peerAddr)
	if err != nil {
		return fmt.Errorf("listen peer %s: %w", *peerAddr, err)
	}
	peerGS := grpc.NewServer()
	peerSrv := peer.NewServer(*dataDir+"/blocks", peer.WithScrubCache(s), peer.WithRebuildHandler(s))
	peerSrv.SetRaftRouter(peer.RaftRouterFunc(func(ctx context.Context, _ uint64, msg raftpb.Message) error {
		return s.RaftStep(ctx, msg)
	}))
	peer.RegisterServer(peerGS, peerSrv)

	adminSrv := admin.New(registry)
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
