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
