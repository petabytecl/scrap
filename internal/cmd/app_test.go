package cmd

import (
	"bytes"
	"context"
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

func TestNewAppLeavesShardAdminRoutesDisabledForMultiShardSingleLocalMember(t *testing.T) {
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
		httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/rewrap/document", strings.NewReader(`{}`)),
		httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/test-hooks/projection-key", strings.NewReader(`{}`)),
	} {
		rec := httptest.NewRecorder()
		app.adminSrv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", req.Method, req.URL.Path, rec.Code)
		}
	}

	_, err = app.publicStore.HeadDocument(context.Background(), "tx-bravo", "doc-b")
	assertUnavailableReason(t, err, storeapi.UnavailableReasonShardRouteUnavailable)
}

func TestNewPeerServerAuthorizesOnlyValidatedLocalShards(t *testing.T) {
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
	topology, err := validateStartupTopology(cfg)
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	shards := &shardSet{ids: append([]uint64(nil), topology.LocalShardIDs...)}
	memberIdentity := scrapdMemberIdentity{MemberHostname: "scrapd-0", MemberID: "member-a"}
	peerSrv := newPeerServer(cfg, shards, appSecurityRuntime{authorizer: security.NewStaticAuthorizer()}, memberIdentity)
	t.Cleanup(func() {
		if err := peerSrv.Close(); err != nil {
			t.Fatalf("peer close: %v", err)
		}
	})
	router := &recordingAppPeerRaftRouter{}
	peerSrv.SetRaftRouter(router)
	ctx := appPeerAuthContext(security.PeerIdentityConfig{
		CellID:         "cell-a",
		MemberHostname: "scrapd-1",
		MemberID:       "member-b",
	})

	if _, err := peerSrv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 7, Message: appPeerRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft local Shard: %v", err)
	}
	if got, want := router.shardIDs, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("routed Shards = %v, want %v", got, want)
	}

	shards.ids[0] = 9
	_, err = peerSrv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 9, Message: appPeerRaftMessage(t)})
	if !errors.Is(err, security.ErrPermissionDenied) || status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ForwardRaft remote Shard = %v (%s), want permission denied", err, status.Code(err))
	}
	if got, want := router.shardIDs, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("routed Shards after remote Shard = %v, want %v", got, want)
	}

	if _, err := peerSrv.ForwardRaft(ctx, &scrapv1.ForwardRaftRequest{ShardId: 7, Message: appPeerRaftMessage(t)}); err != nil {
		t.Fatalf("ForwardRaft local Shard after shardSet mutation: %v", err)
	}
	if got, want := router.shardIDs, []uint64{7, 7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("routed Shards after shardSet mutation = %v, want %v", got, want)
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

func appPeerAuthContext(identity security.PeerIdentityConfig) context.Context {
	ctx := security.ContextWithPrincipal(context.Background(), security.Principal{
		ID:    security.PeerIdentityPrincipalID(identity),
		Roles: security.NewRoleSet(security.RolePeerMember),
	})
	return security.ContextWithPeerIdentity(ctx, identity)
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
