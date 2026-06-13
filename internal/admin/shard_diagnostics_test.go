package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/security"
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
				ShardID:                7,
				Membership:             "local",
				Routes:                 []string{"0-511"},
				State:                  "open",
				Health:                 "ok",
				Readiness:              "ready",
				LeaderState:            "leader",
				IsLeader:               true,
				LeaderID:               1,
				PeerCount:              3,
				PeerHealth:             "configured",
				UploadPressure:         "ok",
				UploadPressureLevel:    0,
				UploadPendingBytes:     0,
				UploadPendingBlocks:    0,
				EvictionPressure:       "ok",
				RestoreFailedBlocks:    0,
				ScannerStatus:          "idle",
				ScannerLagBlocks:       2,
				ScannerInFlightBlocks:  1,
				ScannerLastReason:      "none",
				ScannerScannedBlocks:   3,
				ScannerFailedBlocks:    0,
				ScannerLastUpdatedUnix: 1770000000,
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
	assertShardDiagnosticField(t, shard, "leader_state", "leader")
	assertShardDiagnosticField(t, shard, "is_leader", true)
	assertShardDiagnosticField(t, shard, "leader_id", float64(1))
	assertShardDiagnosticField(t, shard, "peer_count", float64(3))
	assertShardDiagnosticField(t, shard, "peer_health", "configured")
	assertShardDiagnosticField(t, shard, "scanner_status", "idle")
	assertShardDiagnosticField(t, shard, "scanner_lag_blocks", float64(2))
	assertShardDiagnosticField(t, shard, "scanner_in_flight_blocks", float64(1))
	assertShardDiagnosticField(t, shard, "scanner_last_reason", "none")
	assertShardDiagnosticField(t, shard, "scanner_scanned_blocks", float64(3))
	assertShardDiagnosticField(t, shard, "scanner_last_updated_unix", float64(1770000000))
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

func TestServerHealthRequiresAdminReaderBeforeShardDiagnostics(t *testing.T) {
	provider := &shardDiagnosticsProviderStub{}
	authz := security.NewStaticAuthorizer()
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithShardDiagnosticsProvider(provider))

	req := httptest.NewRequestWithContext(adminAuthContext(security.RoleAdminOperator), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body.String())
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestServerHealthRateLimitsBeforeShardDiagnostics(t *testing.T) {
	provider := &shardDiagnosticsProviderStub{}
	authz := security.NewStaticAuthorizer()
	limiter := security.NewRateLimiter(security.RateLimitPolicy{
		Surfaces: []security.RateLimitSurfacePolicy{
			{Surface: security.RateLimitSurfaceAdmin, Limit: 1, Window: time.Minute},
		},
	})
	srv := admin.New(admin.WithAuthorizer(authz), admin.WithRateLimiter(limiter), admin.WithShardDiagnosticsProvider(provider))

	ctx := adminAuthContext(security.RoleAdminReader)
	first := httptest.NewRequestWithContext(ctx, http.MethodGet, "/healthz", nil)
	firstResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstResp, first)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200: %s", firstResp.Code, firstResp.Body.String())
	}

	second := httptest.NewRequestWithContext(ctx, http.MethodGet, "/healthz", nil)
	secondResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondResp, second)
	if secondResp.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429: %s", secondResp.Code, secondResp.Body.String())
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestServerHealthBoundsShardDiagnosticsProviderFailure(t *testing.T) {
	rawErr := "tx-sensitive-raw doc-sensitive.pdf 10.1.2.3:9091 /tmp/sensitive/backend-material credential:material"
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

func TestServerHealthBoundsSuccessfulShardDiagnostics(t *testing.T) {
	provider := &shardDiagnosticsProviderStub{
		snapshot: admin.ShardDiagnostics{
			Status:         "surprising-status",
			Reason:         "/tmp/sensitive/backend-material",
			CellID:         "/tmp/sensitive",
			MemberHostname: "10.1.2.3:9091",
			MemberID:       strings.Repeat("m", 129),
			Shards: []admin.ShardDiagnostic{{
				ShardID:           7,
				Membership:        "local",
				Routes:            []string{"0-511", "/tmp/sensitive"},
				State:             "open",
				Health:            "ok",
				Readiness:         "ready",
				PeerHealth:        "10.1.2.3:9091",
				UploadPressure:    "ok",
				EvictionPressure:  "ok",
				FailureReason:     "credential:material",
				ScannerStatus:     "scanner-error:/tmp/sensitive",
				ScannerLastReason: "rule/material",
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
	body := resp.Body.String()
	for _, forbidden := range []string{"/tmp/sensitive", "10.1.2.3:9091", "backend-material", "credential:material", "scanner-error", "rule/material", strings.Repeat("m", 129)} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("health response leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"redacted"`) || !strings.Contains(body, `"shard_diagnostics":{"status":"degraded"`) {
		t.Fatalf("health response missing bounded diagnostics: %s", body)
	}
}

func assertShardDiagnosticField(t *testing.T, shard map[string]any, key string, want any) {
	t.Helper()
	if shard[key] != want {
		t.Fatalf("shard.%s = %v, want %v", key, shard[key], want)
	}
}
