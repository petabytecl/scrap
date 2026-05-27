package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/petabytecl/scrap/internal/admin"
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

func TestServer_MetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()

	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_scrub_runs_total",
		Help: "Test counter.",
	})
	reg.MustRegister(counter)
	counter.Inc()

	srv := admin.New(reg)
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
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "text/openmetrics") {
		t.Errorf("Content-Type: got %q, want text/plain or text/openmetrics", ct)
	}

	if !strings.Contains(string(body), "test_scrub_runs_total 1") {
		t.Errorf("expected test_scrub_runs_total in body, got:\n%s", string(body))
	}
}

func TestServer_HealthEndpointReportsUploadPressure(t *testing.T) {
	srv := admin.New(prometheus.NewRegistry(), admin.WithUploadPressureProvider(uploadPressureProviderStub{}))
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

func TestServer_TestHookProjectionInjectionDisabledByDefault(t *testing.T) {
	srv := admin.New(prometheus.NewRegistry())
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
	srv := admin.New(prometheus.NewRegistry(), admin.WithProjectionInjector(injector))
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
