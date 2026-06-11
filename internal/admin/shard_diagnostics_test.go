package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/admin"
)

type shardDiagnosticsProviderStub struct {
	calls    int
	snapshot admin.ShardDiagnostics
	err      error
}

func (s *shardDiagnosticsProviderStub) ShardDiagnosticsSnapshot(context.Context) (admin.ShardDiagnostics, error) {
	s.calls++
	return s.snapshot, s.err
}

func TestServerHealthReportsShardDiagnostics(t *testing.T) {
	provider := &shardDiagnosticsProviderStub{
		snapshot: admin.ShardDiagnostics{
			Status:         "ok",
			CellID:         "cell-a",
			MemberHostname: "scrapd-0",
			MemberID:       "member-a",
			Shards: []admin.ShardDiagnostic{{
				ShardID:             7,
				Membership:          "local",
				Routes:              []string{"0-511"},
				State:               "open",
				Health:              "ok",
				Readiness:           "ready",
				IsLeader:            true,
				LeaderID:            1,
				PeerCount:           3,
				PeerHealth:          "configured",
				UploadPressure:      "ok",
				UploadPressureLevel: 0,
				UploadPendingBytes:  0,
				UploadPendingBlocks: 0,
				EvictionPressure:    "ok",
				RestoreFailedBlocks: 0,
			}},
		},
	}
	srv := admin.New(admin.WithShardDiagnosticsProvider(provider))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	diag, ok := got["shard_diagnostics"].(map[string]any)
	if !ok {
		t.Fatalf("shard_diagnostics = %T, want object in %s", got["shard_diagnostics"], resp.Body.String())
	}
	for key, want := range map[string]string{
		"status":          "ok",
		"cell_id":         "cell-a",
		"member_hostname": "scrapd-0",
		"member_id":       "member-a",
	} {
		if diag[key] != want {
			t.Fatalf("shard_diagnostics.%s = %v, want %q", key, diag[key], want)
		}
	}
	shards, ok := diag["shards"].([]any)
	if !ok || len(shards) != 1 {
		t.Fatalf("shards = %#v, want one Shard", diag["shards"])
	}
	shard, ok := shards[0].(map[string]any)
	if !ok {
		t.Fatalf("shard entry = %T, want object", shards[0])
	}
	assertShardDiagnosticField(t, shard, "shard_id", float64(7))
	assertShardDiagnosticField(t, shard, "membership", "local")
	assertShardDiagnosticField(t, shard, "state", "open")
	assertShardDiagnosticField(t, shard, "health", "ok")
	assertShardDiagnosticField(t, shard, "readiness", "ready")
	assertShardDiagnosticField(t, shard, "is_leader", true)
	assertShardDiagnosticField(t, shard, "leader_id", float64(1))
	assertShardDiagnosticField(t, shard, "peer_count", float64(3))
	assertShardDiagnosticField(t, shard, "peer_health", "configured")
}

func TestServerHealthRejectsNonGETBeforeShardDiagnostics(t *testing.T) {
	provider := &shardDiagnosticsProviderStub{}
	srv := admin.New(admin.WithShardDiagnosticsProvider(provider))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405: %s", resp.Code, resp.Body.String())
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestServerHealthBoundsShardDiagnosticsProviderFailure(t *testing.T) {
	rawErr := "tx-secret-raw doc-secret.pdf 10.1.2.3:9091 /tmp/secret/backend-key private-key-material"
	provider := &shardDiagnosticsProviderStub{err: errors.New(rawErr)}
	srv := admin.New(admin.WithShardDiagnosticsProvider(provider))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, forbidden := range strings.Fields(rawErr) {
		if strings.Contains(body, forbidden) {
			t.Fatalf("health response leaked %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{
		`"status":"degraded"`,
		`"shard_diagnostics":{"status":"degraded","reason":"snapshot_unavailable"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("health response missing %s: %s", want, body)
		}
	}
}

func assertShardDiagnosticField(t *testing.T, shard map[string]any, key string, want any) {
	t.Helper()
	if shard[key] != want {
		t.Fatalf("shard.%s = %v, want %v", key, shard[key], want)
	}
}
