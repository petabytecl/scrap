package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
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
	if _, ok := app.publicStore.(*publicStoreRouter); !ok {
		t.Fatalf("multi-Shard public store = %T, want publicStoreRouter", app.publicStore)
	}
}

func TestNewAppLeavesSingleShardAdminRoutesDisabledForMultiShardSingleLocalMember(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.TestHooks = true
	cfg.ShardPlacementFile = writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7, 9],
		"local_shards": [7],
		"ranges": [
			{"shard_id": 7, "start_slot": 0, "end_slot": 511},
			{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
		]
	}`)

	app, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})

	for _, req := range []*http.Request{
		httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/eviction/plans", strings.NewReader(`{}`)),
	} {
		rec := httptest.NewRecorder()
		app.adminSrv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", req.Method, req.URL.Path, rec.Code)
		}
	}

	assertAdminPostStatus(t, app, "/admin/rewrap/document", `{}`, http.StatusBadRequest)
	assertAdminPostStatus(t, app, "/test-hooks/projection-key", `{}`, http.StatusBadRequest)
	assertAppHealthUploadPressure(t, app, "ok", 0)

	_, err = app.publicStore.HeadDocument(context.Background(), "tx-bravo", "doc-b")
	assertUnavailableReason(t, err, storeapi.UnavailableReasonShardRouteUnavailable)
}

func TestNewAppRegistersMultiShardTestHooks(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.TestHooks = true
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

	assertAdminPostStatus(t, app, "/test-hooks/light-scrub", `{}`, http.StatusNotFound)
	assertAdminPostStatus(t, app, "/test-hooks/transit-rotate", `{}`, http.StatusNoContent)
	assertAdminPostStatus(t, app, "/test-hooks/projection-key", `{
		"transaction_id": "tx-multishard-hook",
		"block_id": 1,
		"doc_count": 1,
		"completed": true
	}`, http.StatusNoContent)
	assertAdminPostStatus(t, app, "/admin/rewrap/document", `{}`, http.StatusBadRequest)
	assertAppHealthUploadPressure(t, app, "ok", 0)
}

func assertAdminPostStatus(t *testing.T, app *App, path, body string, want int) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	app.adminSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("POST %s status = %d, want %d", path, rec.Code, want)
	}
}

func assertAppHealthUploadPressure(t *testing.T, app *App, wantPressure string, wantLevel int) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	app.adminSrv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rec.Code)
	}
	var health struct {
		UploadPressure      string `json:"upload_pressure"`
		UploadPressureLevel int    `json:"upload_pressure_level"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if health.UploadPressure != wantPressure || health.UploadPressureLevel != wantLevel {
		t.Fatalf("upload pressure = %q/%d, want %q/%d", health.UploadPressure, health.UploadPressureLevel, wantPressure, wantLevel)
	}
}

func TestNewAppPeerServerAuthorizesOnlyValidatedLocalShards(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.CellID = "cell-a"
	cfg.ShardPlacementFile = writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7, 9],
		"local_shards": [7],
		"ranges": [
			{"shard_id": 7, "start_slot": 0, "end_slot": 511},
			{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
		]
	}`)

	app, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})
	router := &recordingAppPeerRaftRouter{}
	app.peerSrv.SetRaftRouter(router)
	ctx := context.Background()

	assertAppPeerRoutesLocalShard(ctx, t, app, router)
	assertAppShardSetIDsCopy(t, app.shards)
	assertAppPeerDeniesRemoteShard(ctx, t, app, router)
	assertAppPeerNoShardRPCsFailClosed(ctx, t, app)
}

func assertAppPeerRoutesLocalShard(ctx context.Context, t *testing.T, app *App, router *recordingAppPeerRaftRouter) {
	t.Helper()
	if _, err := app.peerSrv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 7, Message: appPeerRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft local Shard: %v", err)
	}
	if got, want := router.shardIDs, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("routed Shards = %v, want %v", got, want)
	}
}

func assertAppShardSetIDsCopy(t *testing.T, shards *shardSet) {
	t.Helper()
	ids := shards.IDs()
	ids[0] = 9
	if got, want := shards.IDs(), []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("app.shards.IDs() after caller mutation = %v, want %v", got, want)
	}
}

func assertAppPeerDeniesRemoteShard(ctx context.Context, t *testing.T, app *App, router *recordingAppPeerRaftRouter) {
	t.Helper()
	_, err := app.peerSrv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 9, Message: appPeerRaftMessage(t)})
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ForwardRaft remote Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if got, want := router.shardIDs, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("routed Shards after remote Shard = %v, want %v", got, want)
	}
}

func assertAppPeerNoShardRPCsFailClosed(ctx context.Context, t *testing.T, app *App) {
	t.Helper()
	if _, err := app.peerSrv.RequestIndexRebuild(ctx, &scrapv1.RequestIndexRebuildRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("RequestIndexRebuild in multi-Shard placement = %v (%s), want failed precondition", err, status.Code(err))
	}
	if _, err := app.peerSrv.ConsistencyCheck(ctx, &scrapv1.ConsistencyCheckRequest{ScrubId: "scrub-secret"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ConsistencyCheck in multi-Shard placement = %v (%s), want failed precondition", err, status.Code(err))
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

type recordingAppPeerRaftRouter struct {
	shardIDs []uint64
}

func (r *recordingAppPeerRaftRouter) RouteRaftMessage(_ context.Context, shardID uint64, _ raftpb.Message) error {
	r.shardIDs = append(r.shardIDs, shardID)
	return nil
}

func appPeerRaftMessage(t *testing.T) []byte {
	t.Helper()
	data, err := (&raftpb.Message{}).Marshal()
	if err != nil {
		t.Fatalf("marshal raft message: %v", err)
	}
	return data
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
