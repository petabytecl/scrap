package observe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestMetricsHandlerExposesCriticalSignalsAtStartup(t *testing.T) {
	resetForTest(t)

	body := scrapeMetrics(t)
	for _, want := range []string{
		"scrap_write_latency_seconds_bucket{outcome=\"success\",le=\"0.005\"} 0",
		"scrap_write_latency_seconds_count{outcome=\"error\"} 0",
		"scrap_verification_total{outcome=\"match\"} 0",
		"scrap_verification_total{outcome=\"mismatch\"} 0",
		"scrap_verification_total{outcome=\"error\"} 0",
		"scrap_verification_total{outcome=\"skipped\"} 0",
		"scrap_backend_probe_total{operation=\"head\",outcome=\"success\"} 0",
		"scrap_backend_probe_total{operation=\"head\",outcome=\"not_found\"} 0",
		"scrap_backend_probe_total{operation=\"head\",outcome=\"error\"} 0",
		"scrap_raft_queue_depth 0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsRecordCriticalSignalOutcomes(t *testing.T) {
	resetForTest(t)

	RecordWriteLatency(OutcomeSuccess, 12*time.Millisecond)
	RecordVerification(VerificationOutcomeMatch)
	RecordVerification(VerificationOutcomeError)
	RecordBackendProbe(BackendOperationHead, OutcomeNotFound)
	IncrementRaftQueueDepth()
	IncrementRaftQueueDepth()
	IncrementRaftQueueDepth()

	body := scrapeMetrics(t)
	for _, want := range []string{
		"scrap_write_latency_seconds_count{outcome=\"success\"} 1",
		"scrap_verification_total{outcome=\"match\"} 1",
		"scrap_verification_total{outcome=\"error\"} 1",
		"scrap_backend_probe_total{operation=\"head\",outcome=\"not_found\"} 1",
		"scrap_raft_queue_depth 3",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func scrapeMetrics(t *testing.T) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler().ServeHTTP(recorder, request)
	testutil.RequireEqualf(t, recorder.Code, 200, "metrics status")
	return recorder.Body.String()
}

func resetForTest(t *testing.T) {
	t.Helper()
	original := defaultMetrics
	defaultMetrics = newMetrics()
	t.Cleanup(func() {
		defaultMetrics = original
	})
}

func TestMetricsHandlerDoesNotExposeHighCardinalityInput(t *testing.T) {
	resetForTest(t)

	RecordBackendProbe("tenant/document/backend-key", OutcomeError)
	body := scrapeMetrics(t)
	if strings.Contains(body, "tenant/document/backend-key") {
		t.Fatalf("metrics body exposed high-cardinality operation label:\n%s", body)
	}
}
