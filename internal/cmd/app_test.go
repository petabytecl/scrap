package cmd

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/shard"
)

// TestAppRunCleanShutdown is a startup/shutdown smoke test. It builds the full
// App on OS-assigned ports, starts serving, then cancels the context (standing
// in for SIGTERM) and asserts Run drains the servers and returns without error.
// This exercises newApp -> Run -> the ordered Shutdown (which closes the shard
// before the outbound transport).
func TestAppRunCleanShutdown(t *testing.T) {
	cfg := Config{
		DataDir:           t.TempDir(),
		ListenAddr:        "127.0.0.1:0",
		PeerAddr:          "127.0.0.1:0",
		AdminAddr:         "127.0.0.1:0",
		BlockSealSize:     shard.DefaultBlockSealSize,
		UploadConcurrency: shard.DefaultUploadConcurrency,
		PeerPort:          defaultPeerPort,
		Namespace:         "default",
		SecurityMode:      security.ModeTest,
		Scrub:             scrub.ParseConfig(),
		UploadPressure:    shard.ParseUploadPressureConfigFromEnv(),
	}
	logger := slog.New(slog.DiscardHandler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := newApp(ctx, cfg, logger, BuildInfo{})
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	// Give the servers a moment to begin serving, then signal shutdown.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on clean shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return within 15s after context cancel")
	}
}

func TestNewAppBuildsTwoShardTopology(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.ShardPlacementFile = writeTwoShardPlacementFile(t)

	app, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})

	if got, want := app.shards.IDs(), []uint64{7, 9}; !uint64SlicesEqual(got, want) {
		t.Fatalf("app.shards.IDs() = %v, want %v", got, want)
	}
	for _, shardID := range []uint64{7, 9} {
		dataDir, ok := app.shards.DataDir(shardID)
		if !ok {
			t.Fatalf("missing data dir for Shard %d", shardID)
		}
		if dataDir != filepath.Join(cfg.DataDir, "shards", "shard-"+strconv.FormatUint(shardID, 10)) {
			t.Fatalf("Shard %d data dir = %q, want per-Shard subdir", shardID, dataDir)
		}
	}
	if app.publicStore == app.shards.singleShardStore() {
		t.Fatal("multi-Shard public store used a single Shard; want fail-closed store until Story 2.3 routing")
	}
}

func TestAppLogStartingRedactsMultiShardEvidence(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	cfg := testAppConfig(t)
	cfg.DataDir = filepath.Join(t.TempDir(), "secret-data")
	cfg.ListenAddr = "10.1.2.3:7777"
	cfg.PeerAddr = "10.1.2.4:7778"
	cfg.AdminAddr = "10.1.2.5:7779"
	topology, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeTest,
		ShardPlacementFile: writeTwoShardPlacementFile(t),
	})
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	app := &App{
		cfg:         cfg,
		logger:      logger,
		shards:      &shardSet{ids: []uint64{7, 9}},
		backendType: "fs",
		peers:       map[uint64]string{1: "10.1.2.4:7778"},
		raftID:      1,
		topology:    topology,
	}

	app.logStarting(context.Background())

	got := log.String()
	for _, forbidden := range []string{cfg.DataDir, cfg.ListenAddr, cfg.PeerAddr, cfg.AdminAddr} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("startup log leaked %q: %s", forbidden, got)
		}
	}
	for _, required := range []string{"route_map", "local_shards", "shard_status"} {
		if !strings.Contains(got, required) {
			t.Fatalf("startup log missing %q: %s", required, got)
		}
	}
}

func TestNewAppRejectsProductionSecurityGatesBeforeSubsystems(t *testing.T) {
	t.Setenv("SCRAP_BACKEND_TYPE", "s3")
	cfg := Config{
		DataDir:           t.TempDir(),
		ListenAddr:        "127.0.0.1:0",
		PeerAddr:          "127.0.0.1:0",
		AdminAddr:         "127.0.0.1:0",
		BlockSealSize:     shard.DefaultBlockSealSize,
		UploadEnabled:     true,
		UploadConcurrency: shard.DefaultUploadConcurrency,
		PeerPort:          defaultPeerPort,
		Namespace:         "default",
		SecurityMode:      security.ModeProduction,
		Scrub:             scrub.ParseConfig(),
		UploadPressure:    shard.ParseUploadPressureConfigFromEnv(),
	}

	_, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err == nil {
		t.Fatal("newApp succeeded, want production security gate error")
	}
	if got := security.ErrorClass(err); got != security.ClassTLSConfig {
		t.Fatalf("ErrorClass() = %q, want %q; err=%v", got, security.ClassTLSConfig, err)
	}
}
