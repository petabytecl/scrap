package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/logbridge"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
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
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		return err
	}

	logger := logbridge.NewLoggerFromEnv(os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := newApp(ctx, cfg, logger)
	if err != nil {
		return err
	}
	return app.Run(ctx)
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
