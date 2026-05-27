package admin_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/petabytecl/scrap/internal/admin"
)

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
