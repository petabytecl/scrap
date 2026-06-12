package cmd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/avscan"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestNewAppWiresShardDiagnosticsForTwoShardTopology(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.CellID = "cell-a"
	cfg.ShardPlacementFile = writeTwoShardPlacementFile(t)

	app := newStartedTestApp(t, cfg)
	resp := requestAppHealth(t, app)
	body := resp.Body.String()
	assertShardDiagnosticsDoNotLeak(t, body, cfg)
	diag := decodeShardDiagnostics(t, resp.Body.Bytes(), body)
	assertAppShardDiagnosticsIdentity(t, diag)
	shards := appShardDiagnosticsEntries(t, diag)
	assertAppShardDiagnostic(t, shards[0], 7, "0-511")
	assertAppShardDiagnostic(t, shards[1], 9, "512-1023")
}

func TestNewAppReportsScannerEngineUnavailableWhenUnconfigured(t *testing.T) {
	cfg := testAppConfig(t)
	cfg.CellID = "cell-a"

	app := newStartedTestApp(t, cfg)
	resp := requestAppHealth(t, app)
	body := resp.Body.String()
	var health map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health["status"] != "degraded" {
		t.Fatalf("health status = %v, want degraded: %s", health["status"], body)
	}
	diag := decodeShardDiagnostics(t, []byte(body), body)
	if diag["status"] != "degraded" {
		t.Fatalf("shard diagnostics status = %v, want degraded: %s", diag["status"], body)
	}
	shardDiag := firstAppShardDiagnostic(t, diag)
	assertJSONFields(t, body, "shard", shardDiag, map[string]any{
		"health":              "degraded",
		"scanner_status":      string(avscan.StatusDegraded),
		"scanner_last_reason": string(avscan.ReasonEngineUnavailable),
	})
	assertPositiveJSONNumber(t, body, shardDiag, "scanner_last_updated_unix")
}

func TestShardDiagnosticsRemoteShardsDoNotDegradeSnapshot(t *testing.T) {
	localTarget := readyShardDiagnosticsTarget()
	source := &fakeShardDiagnosticsSource{
		statuses: []startupShardStatus{
			{ShardID: 7, Membership: "local", Routes: []string{"0-511"}, State: "open"},
			{ShardID: 9, Membership: "remote", Routes: []string{"512-1023"}, State: "not_local"},
		},
		targets: map[uint64]*fakeShardDiagnosticsTarget{7: localTarget},
	}
	provider := appShardDiagnosticsProvider{
		cellID:    "cell-a",
		member:    scrapdMemberIdentity{MemberHostname: "scrapd-0", MemberID: "member-a"},
		shards:    source,
		peerCount: 3,
	}

	got, err := provider.ShardDiagnosticsSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ShardDiagnosticsSnapshot: %v", err)
	}
	if got.Status != admin.ShardDiagnosticsStatusOK {
		t.Fatalf("status = %s, want ok: %#v", got.Status, got)
	}
	remote := shardDiagnosticByID(t, got.Shards, 9)
	if remote.Health != shardDiagnosticsStateNotLocal || remote.Readiness != shardDiagnosticsStateNotLocal {
		t.Fatalf("remote health/readiness = %s/%s, want not_local/not_local", remote.Health, remote.Readiness)
	}
	if remote.LeaderState != shardDiagnosticsStateNotLocal {
		t.Fatalf("remote leader state = %s, want not_local", remote.LeaderState)
	}
	if localTarget.readinessCalls != 1 || localTarget.uploadCalls != 1 || localTarget.evictionCalls != 1 {
		t.Fatalf("local read-only calls = readiness:%d upload:%d eviction:%d, want 1 each", localTarget.readinessCalls, localTarget.uploadCalls, localTarget.evictionCalls)
	}
}

func TestShardDiagnosticsPressureDoesNotOverrideReadiness(t *testing.T) {
	for _, tt := range []struct {
		name       string
		level      shard.UploadPressureLevel
		levelName  string
		wantStatus string
		wantHealth string
		wantReason string
	}{
		{
			name:       "warn",
			level:      shard.UploadPressureLevelWarn,
			levelName:  "warn",
			wantStatus: admin.ShardDiagnosticsStatusOK,
			wantHealth: shardDiagnosticsHealthOK,
		},
		{
			name:       "pressure",
			level:      shard.UploadPressureLevelPressure,
			levelName:  "pressure",
			wantStatus: admin.ShardDiagnosticsStatusDegraded,
			wantHealth: shardDiagnosticsHealthDegraded,
			wantReason: "pressure",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := readyShardDiagnosticsTarget()
			target.uploadLevel = int(tt.level)
			target.uploadName = tt.levelName
			got := snapshotFromFakeTarget(t, target)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tt.wantStatus)
			}
			shardDiag := shardDiagnosticByID(t, got.Shards, 7)
			if shardDiag.Health != tt.wantHealth {
				t.Fatalf("health = %s, want %s", shardDiag.Health, tt.wantHealth)
			}
			if shardDiag.Readiness != shardDiagnosticsReadinessReady {
				t.Fatalf("readiness = %s, want ready", shardDiag.Readiness)
			}
			if shardDiag.FailureReason != tt.wantReason {
				t.Fatalf("failure reason = %s, want %s", shardDiag.FailureReason, tt.wantReason)
			}
		})
	}
}

func TestShardDiagnosticsNoLeaderReportsBoundedFailure(t *testing.T) {
	target := readyShardDiagnosticsTarget()
	target.leaderID = 0

	got := snapshotFromFakeTarget(t, target)
	if got.Status != admin.ShardDiagnosticsStatusDegraded {
		t.Fatalf("status = %s, want degraded", got.Status)
	}
	shardDiag := shardDiagnosticByID(t, got.Shards, 7)
	if shardDiag.FailureReason != shardDiagnosticsReasonNoLeader {
		t.Fatalf("failure reason = %s, want no_leader", shardDiag.FailureReason)
	}
	if shardDiag.LeaderState != shardDiagnosticsLeaderUnknown {
		t.Fatalf("leader state = %s, want unknown", shardDiag.LeaderState)
	}
	if shardDiag.Readiness != shardDiagnosticsReadinessReady {
		t.Fatalf("readiness = %s, want ready", shardDiag.Readiness)
	}
}

func TestShardDiagnosticsScannerDegradesSnapshot(t *testing.T) {
	target := readyShardDiagnosticsTarget()
	target.scanner = avscan.Snapshot{
		Status:         avscan.StatusDegraded,
		LastReason:     avscan.ReasonEngineUnavailable,
		LagBlocks:      3,
		InFlightBlocks: 1,
		ScannedBlocks:  2,
		FailedBlocks:   1,
		LastUpdated:    time.Unix(1770000000, 0),
	}

	got := snapshotFromFakeTarget(t, target)
	if got.Status != admin.ShardDiagnosticsStatusDegraded {
		t.Fatalf("status = %s, want degraded", got.Status)
	}
	shardDiag := shardDiagnosticByID(t, got.Shards, 7)
	if shardDiag.Health != shardDiagnosticsHealthDegraded {
		t.Fatalf("health = %s, want degraded", shardDiag.Health)
	}
	if shardDiag.FailureReason != string(avscan.ReasonEngineUnavailable) {
		t.Fatalf("failure reason = %s, want %s", shardDiag.FailureReason, avscan.ReasonEngineUnavailable)
	}
	if shardDiag.ScannerStatus != string(avscan.StatusDegraded) {
		t.Fatalf("scanner status = %s, want %s", shardDiag.ScannerStatus, avscan.StatusDegraded)
	}
	if shardDiag.ScannerLagBlocks != 3 || shardDiag.ScannerInFlightBlocks != 1 {
		t.Fatalf("scanner lag/in-flight = %d/%d, want 3/1", shardDiag.ScannerLagBlocks, shardDiag.ScannerInFlightBlocks)
	}
	if shardDiag.ScannerScannedBlocks != 2 || shardDiag.ScannerFailedBlocks != 1 {
		t.Fatalf("scanner scanned/failed = %d/%d, want 2/1", shardDiag.ScannerScannedBlocks, shardDiag.ScannerFailedBlocks)
	}
	if shardDiag.ScannerLastUpdatedUnix != 1770000000 {
		t.Fatalf("scanner last updated = %d, want 1770000000", shardDiag.ScannerLastUpdatedUnix)
	}
}

func newStartedTestApp(t *testing.T, cfg Config) *App {
	t.Helper()
	app, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})
	return app
}

func requestAppHealth(t *testing.T, app *App) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	app.adminSrv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	return resp
}

func assertShardDiagnosticsDoNotLeak(t *testing.T, body string, cfg Config) {
	t.Helper()
	for _, forbidden := range []string{cfg.DataDir, filepath.Join(cfg.DataDir, "shards"), "127.0.0.1:"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("admin Shard diagnostics leaked %q: %s", forbidden, body)
		}
	}
}

func decodeShardDiagnostics(t *testing.T, bodyBytes []byte, body string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(bodyBytes, &got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	diag, ok := got["shard_diagnostics"].(map[string]any)
	if !ok {
		t.Fatalf("missing shard_diagnostics in %s", body)
	}
	return diag
}

func assertAppShardDiagnosticsIdentity(t *testing.T, diag map[string]any) {
	t.Helper()
	if diag["cell_id"] != "cell-a" {
		t.Fatalf("cell_id = %v, want cell-a", diag["cell_id"])
	}
	if diag["member_hostname"] == "" || diag["member_id"] == "" {
		t.Fatalf("member identity missing from diagnostics: %#v", diag)
	}
}

func appShardDiagnosticsEntries(t *testing.T, diag map[string]any) []any {
	t.Helper()
	shards, ok := diag["shards"].([]any)
	if !ok || len(shards) != 2 {
		t.Fatalf("shards = %#v, want two Shards", diag["shards"])
	}
	return shards
}

func firstAppShardDiagnostic(t *testing.T, diag map[string]any) map[string]any {
	t.Helper()
	shards, ok := diag["shards"].([]any)
	if !ok || len(shards) == 0 {
		t.Fatalf("shards = %#v, want at least one Shard", diag["shards"])
	}
	shard, ok := shards[0].(map[string]any)
	if !ok {
		t.Fatalf("shard diagnostic = %T, want object", shards[0])
	}
	return shard
}

func assertJSONFields(t *testing.T, body, prefix string, object, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if object[key] != value {
			t.Fatalf("%s.%s = %v, want %v in %s", prefix, key, object[key], value, body)
		}
	}
}

func assertPositiveJSONNumber(t *testing.T, body string, object map[string]any, key string) {
	t.Helper()
	got, ok := object[key].(float64)
	if !ok || got <= 0 {
		t.Fatalf("%s = %v, want positive number in %s", key, object[key], body)
	}
}

func assertAppShardDiagnostic(t *testing.T, raw any, shardID int, route string) {
	t.Helper()
	shard, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("shard diagnostic = %T, want object", raw)
	}
	assertAppShardDiagnosticIdentity(t, shard, shardID)
	assertAppShardDiagnosticRoute(t, shard, route)
	assertAppShardDiagnosticHealth(t, shard)
}

func assertAppShardDiagnosticIdentity(t *testing.T, shard map[string]any, shardID int) {
	t.Helper()
	if shard["shard_id"] != float64(shardID) {
		t.Fatalf("shard_id = %v, want %d", shard["shard_id"], shardID)
	}
	if shard["membership"] != "local" {
		t.Fatalf("membership = %v, want local", shard["membership"])
	}
	if shard["state"] != "open" {
		t.Fatalf("state = %v, want open", shard["state"])
	}
}

func assertAppShardDiagnosticRoute(t *testing.T, shard map[string]any, route string) {
	t.Helper()
	routes, ok := shard["routes"].([]any)
	if !ok || len(routes) != 1 || routes[0] != route {
		t.Fatalf("routes = %#v, want [%s]", shard["routes"], route)
	}
}

func assertAppShardDiagnosticHealth(t *testing.T, shard map[string]any) {
	t.Helper()
	if shard["health"] == "" || shard["readiness"] == "" {
		t.Fatalf("health/readiness missing: %#v", shard)
	}
	if shard["peer_count"] == nil || shard["peer_health"] == "" {
		t.Fatalf("peer diagnostics missing: %#v", shard)
	}
}

func snapshotFromFakeTarget(t *testing.T, target *fakeShardDiagnosticsTarget) admin.ShardDiagnostics {
	t.Helper()
	source := &fakeShardDiagnosticsSource{
		statuses: []startupShardStatus{{ShardID: 7, Membership: "local", Routes: []string{"0-1023"}, State: "open"}},
		targets:  map[uint64]*fakeShardDiagnosticsTarget{7: target},
	}
	provider := appShardDiagnosticsProvider{shards: source, peerCount: 1}
	got, err := provider.ShardDiagnosticsSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ShardDiagnosticsSnapshot: %v", err)
	}
	return got
}

func readyShardDiagnosticsTarget() *fakeShardDiagnosticsTarget {
	return &fakeShardDiagnosticsTarget{
		leaderID:   1,
		uploadName: shardDiagnosticsHealthOK,
		eviction:   eviction.HealthSnapshot{Pressure: eviction.HealthPressureOK},
	}
}

func shardDiagnosticByID(t *testing.T, shards []admin.ShardDiagnostic, shardID uint64) admin.ShardDiagnostic {
	t.Helper()
	for _, shard := range shards {
		if shard.ShardID == shardID {
			return shard
		}
	}
	t.Fatalf("missing Shard %d in %#v", shardID, shards)
	return admin.ShardDiagnostic{}
}

type fakeShardDiagnosticsSource struct {
	statuses []startupShardStatus
	targets  map[uint64]*fakeShardDiagnosticsTarget
}

func (s *fakeShardDiagnosticsSource) StartupStatus(startupTopology) []startupShardStatus {
	out := make([]startupShardStatus, len(s.statuses))
	for i, status := range s.statuses {
		status.Routes = append([]string(nil), status.Routes...)
		out[i] = status
	}
	return out
}

func (s *fakeShardDiagnosticsSource) diagnosticsTarget(shardID uint64) (shardDiagnosticsTarget, bool) {
	target, ok := s.targets[shardID]
	return target, ok
}

type fakeShardDiagnosticsTarget struct {
	readinessErr error
	leader       bool
	leaderID     uint64
	uploadLevel  int
	uploadName   string
	eviction     eviction.HealthSnapshot
	evictionErr  error
	scanner      avscan.Snapshot

	readinessCalls int
	leaderCalls    int
	leaderIDCalls  int
	uploadCalls    int
	evictionCalls  int
}

func (t *fakeShardDiagnosticsTarget) CheckReadiness(context.Context) error {
	t.readinessCalls++
	return t.readinessErr
}

func (t *fakeShardDiagnosticsTarget) IsLeader() bool {
	t.leaderCalls++
	return t.leader
}

func (t *fakeShardDiagnosticsTarget) LeaderID() uint64 {
	t.leaderIDCalls++
	return t.leaderID
}

func (t *fakeShardDiagnosticsTarget) UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int) {
	t.uploadCalls++
	return t.uploadLevel, t.uploadName, 0, 0
}

func (t *fakeShardDiagnosticsTarget) EvictionHealthSnapshot(context.Context) (eviction.HealthSnapshot, error) {
	t.evictionCalls++
	return t.eviction, t.evictionErr
}

func (t *fakeShardDiagnosticsTarget) ContentScannerSnapshot() avscan.Snapshot {
	return t.scanner
}
