package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/logbridge"
	scrapraft "github.com/petabytecl/scrap/internal/raft"
)

const (
	shutdownTimeout = 5 * time.Second
	defaultPeerPort = 9091
)

// BuildInfo carries the metadata embedded by the scrapd build.
type BuildInfo struct {
	Version   string
	BuildSHA  string
	BuildTime string
}

func (b BuildInfo) withDefaults() BuildInfo {
	if b.Version == "" {
		b.Version = "dev"
	}
	if b.BuildSHA == "" {
		b.BuildSHA = "unknown"
	}
	if b.BuildTime == "" {
		b.BuildTime = "unknown"
	}
	return b
}

// Run assembles and runs scrapd from command-line arguments and environment.
func Run(args []string, stderr io.Writer, build BuildInfo) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}

	logger := logbridge.NewLoggerFromEnv(stderr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := newApp(ctx, cfg, logger, build.withDefaults())
	if err != nil {
		return err
	}
	return app.Run(ctx)
}

func openConfiguredUploadBackend(ctx context.Context, cfg Config) (backend.Backend, string, error) {
	if !cfg.UploadEnabled {
		return nil, "", nil
	}
	return openUploadBackend(ctx, cfg)
}

func openUploadBackend(ctx context.Context, cfg Config) (backend.Backend, string, error) {
	backendType := os.Getenv("SCRAP_BACKEND_TYPE")
	if backendType == "" {
		backendType = "fs"
	}
	if err := validateProductionBackend(cfg, backendType); err != nil {
		return nil, "", err
	}

	switch backendType {
	case "fs":
		return backend.NewFS(filepath.Join(cfg.DataDir, "backend")), backendType, nil
	case "s3":
		s3Cfg, err := backend.ParseS3ConfigFromEnv()
		if err != nil {
			return nil, "", fmt.Errorf("parse S3 backend config: %w", err)
		}
		store, err := backend.NewS3FromConfig(ctx, s3Cfg)
		if err != nil {
			return nil, "", fmt.Errorf("open S3 backend: %w", err)
		}
		return store, backendType, nil
	default:
		return nil, "", fmt.Errorf("unsupported SCRAP_BACKEND_TYPE %q", backendType)
	}
}

// validateProductionBackend rejects Member-local filesystem Backend in
// production multi-Member or eviction-enabled Cells (ADR 0019 / H-10).
func validateProductionBackend(cfg Config, backendType string) error {
	if !cfg.SecurityMode.IsProduction() || backendType != "fs" {
		return nil
	}
	if productionMultiMemberTopology(cfg) || cfg.Eviction.Enabled {
		return errors.New("production mode rejects filesystem Backend for multi-Member or eviction-enabled Cells")
	}
	return nil
}

func productionMultiMemberTopology(cfg Config) bool {
	if cfg.Replicas > 1 {
		return true
	}
	if cfg.PeersFlag == "" {
		return false
	}
	peers, err := scrapraft.ParsePeersFlag(cfg.PeersFlag)
	return err == nil && len(peers) > 1
}

func resolvePeers(cfg Config) (map[uint64]string, uint64, error) {
	peers, raftID, err := resolvePeersUnchecked(cfg)
	if err != nil {
		return nil, 0, err
	}
	if err := validateProductionMembership(cfg, peers); err != nil {
		return nil, 0, err
	}
	return peers, raftID, nil
}

func resolvePeersUnchecked(cfg Config) (map[uint64]string, uint64, error) {
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
		return resolveK8sPeers(cfg)

	case cfg.Replicas > 0 || cfg.HeadlessService != "":
		// Half-specified cluster identity: fail closed rather than silently
		// falling through to the lone-node default, which would make a pod that
		// is meant to be clustered bootstrap as a single-voter raft ID 1.
		return nil, 0, halfSpecifiedClusterError(cfg)

	default:
		return map[uint64]string{1: "localhost:9091"}, 1, nil
	}
}

// validateProductionMembership requires explicit multi-voter topology before
// production readiness or write admission (ADR 0019 / H-09).
func validateProductionMembership(cfg Config, peers map[uint64]string) error {
	if !cfg.SecurityMode.IsProduction() {
		return nil
	}
	const minProductionVoters = 2
	if len(peers) < minProductionVoters {
		return errors.New("production mode requires explicit multi-voter membership")
	}
	if cfg.PeersFlag == "" && (cfg.Replicas <= 1 || cfg.HeadlessService == "") {
		return errors.New("production mode requires explicit --peers or SCRAP_REPLICAS>1 with SCRAP_HEADLESS_SERVICE")
	}
	return nil
}

func resolveK8sPeers(cfg Config) (map[uint64]string, uint64, error) {
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
}

func halfSpecifiedClusterError(cfg Config) error {
	if cfg.HeadlessService == "" {
		return errors.New("SCRAP_HEADLESS_SERVICE is required when SCRAP_REPLICAS is set")
	}
	return errors.New("SCRAP_REPLICAS must be greater than 0 when SCRAP_HEADLESS_SERVICE is set")
}

func resolveClientAddrs(cfg Config, peers map[uint64]string) (map[uint64]string, error) { //nolint:cyclop // production client-addr resolution has several mutually exclusive discovery paths
	if cfg.ClientAddrsFlag != "" {
		addrs, err := scrapraft.ParsePeersFlag(cfg.ClientAddrsFlag)
		if err != nil {
			return nil, fmt.Errorf("invalid --client-addrs: %w", err)
		}
		if err := validateClientAddrCoverage(peers, addrs); err != nil {
			return nil, err
		}
		return addrs, nil
	}
	if cfg.SecurityMode.IsProduction() && (cfg.Replicas <= 0 || cfg.HeadlessService == "") {
		return nil, errors.New("production mode requires explicit --client-addrs (or SCRAP_CLIENT_ADDRS) outside Kubernetes discovery")
	}
	if cfg.Replicas > 0 && cfg.HeadlessService != "" {
		clientPort, err := portFromListenAddr(cfg.ListenAddr)
		if err == nil && clientPort > 0 {
			return scrapraft.BuildK8sPeers(cfg.Replicas, cfg.HeadlessService, cfg.Namespace, clientPort), nil
		}
	}
	return copyPeerAddrs(peers), nil
}

func validateClientAddrCoverage(peers, addrs map[uint64]string) error {
	for id := range peers {
		if strings.TrimSpace(addrs[id]) == "" {
			return fmt.Errorf("client address missing for raft member %d", id)
		}
	}
	return nil
}

func portFromListenAddr(addr string) (int, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("split listen address %q: %w", addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("parse listen port %q: %w", port, err)
	}
	return n, nil
}

func copyPeerAddrs(peers map[uint64]string) map[uint64]string {
	copied := make(map[uint64]string, len(peers))
	for id, addr := range peers {
		copied[id] = addr
	}
	return copied
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
