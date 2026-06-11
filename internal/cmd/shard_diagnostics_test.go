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
