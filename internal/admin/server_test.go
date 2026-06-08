package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/admin"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/rewrap"
	"github.com/petabytecl/scrap/internal/security"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type projectionInjectorStub struct {
	txID      string
	blockID   uint64
	docCount  uint16
	completed bool
}

type uploadPressureProviderStub struct{}

func (s *projectionInjectorStub) InjectProjectionKey(_ context.Context, txID string, blockID uint64, docCount uint16, completed bool) error {
	s.txID = txID
	s.blockID = blockID
	s.docCount = docCount
	s.completed = completed
	return nil
}

func (uploadPressureProviderStub) UploadPressureSnapshot() (level int, levelName string, pendingBytes int64, pendingBlocks int) {
	return 2, "pressure", 1024, 3
}

type evictionHealthProviderStub struct{}

type rewrapServiceStub struct {
	calls  int
	req    rewrap.Request
	result rewrap.Result
	err    error
	health rewrap.HealthSnapshot
}

func (evictionHealthProviderStub) EvictionHealthSnapshot(context.Context) (eviction.HealthSnapshot, error) {
	return eviction.HealthSnapshot{
		Pressure:               "degraded",
		EvictedBlocks:          4,
		EvictedBytes:           8192,
		HotCleanupNeededBlocks: 1,
		MetadataLossBlocks:     2,
		UnexpectedLossBlocks:   3,
		QuarantinedBlocks:      5,
		RestoreFailedBlocks:    6,
		RestoreFailuresByReason: map[string]int{
			eviction.RestoreFailureBackendUnavailable: 6,
		},
	}, nil
}

func (s *rewrapServiceStub) RewrapDocument(_ context.Context, req rewrap.Request) (rewrap.Result, error) {
	s.calls++
	s.req = req
	return s.result, s.err
}

func (s *rewrapServiceStub) RewrapHealthSnapshot() rewrap.HealthSnapshot {
	return s.health
}

func TestServer_HealthEndpointReportsUploadPressure(t *testing.T) {
	srv := admin.New(admin.WithUploadPressureProvider(uploadPressureProviderStub{}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if got["upload_pressure_level"] != float64(2) {
		t.Fatalf("upload_pressure_level = %v, want 2", got["upload_pressure_level"])
	}
	if got["upload_pressure"] != "pressure" {
		t.Fatalf("upload_pressure = %v, want pressure", got["upload_pressure"])
	}
	if got["upload_pending_bytes"] != float64(1024) {
		t.Fatalf("upload_pending_bytes = %v, want 1024", got["upload_pending_bytes"])
	}
	if got["upload_pending_blocks"] != float64(3) {
		t.Fatalf("upload_pending_blocks = %v, want 3", got["upload_pending_blocks"])
	}
}

func TestServer_HealthEndpointReportsSecurityMode(t *testing.T) {
	readiness := security.ProductionReadinessForMode(security.ModeDevelopment)
	srv := admin.New(admin.WithSecurityStatus(security.ModeDevelopment, readiness))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	for key, want := range map[string]string{
		"status":                      "ok",
		"security_mode":               "development",
		"production_readiness_status": "not_ready",
		"production_readiness_reason": "non_production_security_mode",
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %q", key, got[key], want)
		}
	}
}

func TestServer_HealthEndpointReportsEvictionLifecycleSeparately(t *testing.T) {
	srv := admin.New(admin.WithEvictionHealthProvider(evictionHealthProviderStub{}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}

	assertHealthNumber(t, got, "evicted_blocks", 4)
	assertHealthNumber(t, got, "evicted_bytes", 8192)
	assertHealthNumber(t, got, "hot_cleanup_needed_blocks", 1)
	assertHealthNumber(t, got, "metadata_loss_blocks", 2)
	assertHealthNumber(t, got, "unexpected_loss_blocks", 3)
	assertHealthNumber(t, got, "quarantined_blocks", 5)
	assertHealthNumber(t, got, "restore_failed_blocks", 6)
	if got["eviction_pressure"] != "degraded" {
		t.Fatalf("eviction_pressure = %v, want degraded", got["eviction_pressure"])
	}
	reasons, ok := got["restore_failures_by_reason"].(map[string]any)
	if !ok {
		t.Fatalf("restore_failures_by_reason = %T, want object", got["restore_failures_by_reason"])
	}
	if reasons["backend_restore_unavailable"] != float64(6) {
		t.Fatalf("backend restore failures = %v, want 6", reasons["backend_restore_unavailable"])
	}
}

func TestServer_HealthEndpointReportsBoundedRewrapStatus(t *testing.T) {
	rewrappedAt := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	service := &rewrapServiceStub{
		health: rewrap.HealthSnapshot{
			Status:           rewrap.StatusDegraded,
			LastResult:       rewrap.StatusFailed,
			LastReason:       rewrap.ReasonCryptoUnavailable,
			LastTransitMount: "transit",
			LastTransitKey:   "documents",
			LastOldVersion:   1,
			LastNewVersion:   1,
			LastAt:           rewrappedAt,
			FailuresByReason: map[string]int{
				rewrap.ReasonCryptoUnavailable: 2,
			},
		},
	}
	srv := admin.New(admin.WithRewrapService(service))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	assertNoSensitiveRewrapFields(t, body, "transaction_id", "document_name")

	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	for key, want := range map[string]string{
		"status":                    "degraded",
		"rewrap_status":             rewrap.StatusDegraded,
		"rewrap_last_result":        rewrap.StatusFailed,
		"rewrap_last_reason":        rewrap.ReasonCryptoUnavailable,
		"rewrap_last_transit_mount": "transit",
		"rewrap_last_transit_key":   "documents",
		"rewrap_last_at":            "2026-06-08T12:00:00Z",
	} {
		if got[key] != want {
			t.Fatalf("%s = %v, want %q", key, got[key], want)
		}
	}
	assertHealthNumber(t, got, "rewrap_last_old_version", 1)
	assertHealthNumber(t, got, "rewrap_last_new_version", 1)
	reasons, ok := got["rewrap_failures_by_reason"].(map[string]any)
	if !ok {
		t.Fatalf("rewrap_failures_by_reason = %T, want object", got["rewrap_failures_by_reason"])
	}
	if reasons[rewrap.ReasonCryptoUnavailable] != float64(2) {
		t.Fatalf("crypto unavailable failures = %v, want 2", reasons[rewrap.ReasonCryptoUnavailable])
	}
}

func TestServer_RewrapDocumentEndpointCallsServiceWithSafeResponse(t *testing.T) {
	service := &rewrapServiceStub{
		result: rewrap.Result{
			Status:        rewrap.StatusOK,
			Reason:        rewrap.ReasonOK,
			Changed:       true,
			TransactionID: "tx-rewrap",
			DocumentName:  "doc.xml",
			BlockID:       7,
			TransitMount:  "transit",
			TransitKey:    "documents",
			OldKeyVersion: 1,
			NewKeyVersion: 2,
		},
	}
	srv := admin.New(admin.WithRewrapService(service))

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/admin/rewrap/document",
		strings.NewReader(`{"transaction_id":"tx-rewrap","document_name":"doc.xml","key_version":2,"reason":"rotate"}`),
	)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body.String())
	}
	if service.calls != 1 {
		t.Fatalf("rewrap calls = %d, want 1", service.calls)
	}
	if service.req.TransactionID != "tx-rewrap" || service.req.DocumentName != "doc.xml" || service.req.KeyVersion != 2 || service.req.Reason != "rotate" {
		t.Fatalf("rewrap request mismatch: %+v", service.req)
	}
	body := resp.Body.String()
	assertNoSensitiveRewrapFields(t, body)
	assertRewrapResponse(t, body, rewrap.StatusOK, 1, 2, true)
}

func TestServer_RewrapDocumentEndpointRejectsInvalidRequest(t *testing.T) {
	service := &rewrapServiceStub{}
	srv := admin.New(admin.WithRewrapService(service))

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/rewrap/document", strings.NewReader(`{"transaction_id":`))
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.Code, resp.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("rewrap calls = %d, want 0", service.calls)
	}
}

func TestServer_RewrapDocumentEndpointRejectsNonPost(t *testing.T) {
	service := &rewrapServiceStub{}
	srv := admin.New(admin.WithRewrapService(service))

	resp := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/rewrap/document", nil)
	srv.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405: %s", resp.Code, resp.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("rewrap calls = %d, want 0", service.calls)
	}
}

func TestServer_RewrapDocumentEndpointMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{name: "invalid request", err: rewrap.ErrInvalidRequest, status: http.StatusBadRequest, reason: rewrap.ReasonInvalidRequest},
		{name: "not encrypted", err: rewrap.ErrNotEncrypted, status: http.StatusPreconditionFailed, reason: rewrap.ReasonNotEncrypted},
		{name: "data loss", err: storeapi.ErrDataLoss, status: http.StatusPreconditionFailed, reason: rewrap.ReasonDataLoss},
		{name: "not found", err: storeapi.ErrNotFound, status: http.StatusNotFound, reason: rewrap.ReasonNotFound},
		{
			name:   "unavailable",
			err:    storeapi.NewUnavailable(storeapi.UnavailableReasonCryptoUnavailable, "crypto unavailable"),
			status: http.StatusServiceUnavailable,
			reason: rewrap.ReasonCryptoUnavailable,
		},
		{name: "internal", err: errors.New("boom"), status: http.StatusInternalServerError, reason: rewrap.ReasonInternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &rewrapServiceStub{
				result: rewrap.Result{Status: rewrap.StatusFailed, Reason: tt.reason},
				err:    tt.err,
			}
			srv := admin.New(admin.WithRewrapService(service))
			resp := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/rewrap/document", strings.NewReader(`{"transaction_id":"tx","document_name":"doc.xml"}`))

			srv.Handler().ServeHTTP(resp, req)

			if resp.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.Code, tt.status, resp.Body.String())
			}
			assertNoSensitiveRewrapFields(t, resp.Body.String())
			assertRewrapResponse(t, resp.Body.String(), rewrap.StatusFailed, 0, 0, false)
		})
	}
}

func assertNoSensitiveRewrapFields(t *testing.T, body string, extraForbidden ...string) {
	t.Helper()

	forbidden := append([]string{"wrapped_data_key", "ciphertext", "plaintext"}, extraForbidden...)
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Fatalf("rewrap evidence leaked %q: %s", value, body)
		}
	}
}

func assertRewrapResponse(t *testing.T, body, status string, oldVersion, newVersion int, changed bool) {
	t.Helper()

	var got rewrap.Result
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode rewrap response: %v", err)
	}
	if got.Status != status || got.OldKeyVersion != oldVersion || got.NewKeyVersion != newVersion || got.Changed != changed {
		t.Fatalf("rewrap response mismatch: %+v", got)
	}
}

func assertHealthNumber(t *testing.T, got map[string]any, key string, want float64) {
	t.Helper()

	if got[key] != want {
		t.Fatalf("%s = %v, want %.0f", key, got[key], want)
	}
}

func TestServer_HealthEndpointLogsEvidenceMarker(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := admin.New(admin.WithLogger(logger))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Scrap-Evidence-Marker", "scrap-evidence-20260529T120000Z-abc1234")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	got := logs.String()
	if !strings.Contains(got, `"msg":"evidence log probe"`) {
		t.Fatalf("missing evidence log probe message: %s", got)
	}
	if !strings.Contains(got, `"evidence_marker":"scrap-evidence-20260529T120000Z-abc1234"`) {
		t.Fatalf("missing evidence marker attribute: %s", got)
	}
}

func TestServer_HealthEndpointRejectsInvalidEvidenceMarker(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := admin.New(admin.WithLogger(logger))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Scrap-Evidence-Marker", "bad marker with spaces")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	if logs.Len() > 0 {
		t.Fatalf("invalid marker should not be logged: %s", logs.String())
	}
}

func TestServer_TestHookProjectionInjectionDisabledByDefault(t *testing.T) {
	srv := admin.New()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/test-hooks/projection-key", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST test hook: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestServer_TestHookProjectionInjection(t *testing.T) {
	injector := &projectionInjectorStub{}
	srv := admin.New(admin.WithProjectionInjector(injector))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := map[string]any{
		"transaction_id": "tx-divergent",
		"block_id":       42,
		"doc_count":      3,
		"completed":      true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/test-hooks/projection-key", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST test hook: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", resp.StatusCode)
	}
	if injector.txID != "tx-divergent" || injector.blockID != 42 || injector.docCount != 3 || !injector.completed {
		t.Fatalf("injected payload mismatch: %+v", injector)
	}
}

func TestServer_PprofDisabledByDefault(t *testing.T) {
	srv := admin.New()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/debug/pprof/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /debug/pprof/: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pprof should be 404 when disabled, got %d", resp.StatusCode)
	}
}

func TestServer_PprofEnabledWithOption(t *testing.T) {
	srv := admin.New(admin.WithPprof())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/debug/pprof/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /debug/pprof/: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pprof should be 200 when enabled, got %d", resp.StatusCode)
	}
}

func TestServer_PprofRejectsNonGet(t *testing.T) {
	srv := admin.New(admin.WithPprof())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Neither POST nor HEAD may invoke a profiling handler. HEAD is the subtle
	// one: net/http's ServeMux routes it to a GET handler, so without an explicit
	// guard HEAD /debug/pprof/profile would still start CPU collection.
	for _, method := range []string{http.MethodPost, http.MethodHead} {
		req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+"/debug/pprof/profile", nil)
		if err != nil {
			t.Fatalf("new %s request: %v", method, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s /debug/pprof/profile: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s pprof should be 405, got %d", method, resp.StatusCode)
		}
	}
}

func TestServer_PprofSymbolAcceptsPost(t *testing.T) {
	srv := admin.New(admin.WithPprof())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// `go tool pprof` POSTs the address list in the request body whenever it
	// exceeds URL length limits; /symbol must not be gated to GET. An empty body
	// is a valid request that the stdlib handler answers with 200.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/debug/pprof/symbol", http.NoBody)
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /debug/pprof/symbol: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /debug/pprof/symbol should be 200, got %d", resp.StatusCode)
	}
}

func TestServer_MetricsEndpoint(t *testing.T) {
	const want = "scrap_test_metric 1\n"
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(want))
	})
	srv := admin.New(admin.WithMetrics(handler))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/metrics", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics: got %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != want {
		t.Fatalf("/metrics body = %q, want %q", string(body), want)
	}
}

func TestServer_MetricsAbsentByDefault(t *testing.T) {
	srv := admin.New()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/metrics", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/metrics should be 404 without WithMetrics, got %d", resp.StatusCode)
	}
}
