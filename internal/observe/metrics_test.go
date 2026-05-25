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
		"scrap_block_append_queue_depth 0",
		"scrap_block_sync_batch_size_count 0",
		"scrap_block_sync_latency_seconds_count{outcome=\"success\"} 0",
		"scrap_block_sync_latency_seconds_count{outcome=\"error\"} 0",
		"scrap_metadata_command_batch_size_count 0",
		"scrap_metadata_command_sync_latency_seconds_count{outcome=\"success\"} 0",
		"scrap_metadata_command_sync_latency_seconds_count{outcome=\"error\"} 0",
		"scrap_batch_failures_total{component=\"blockstore\",stage=\"sync\"} 0",
		"scrap_batch_failures_total{component=\"raftmeta\",stage=\"sync\"} 0",
		"scrap_batch_failures_total{component=\"raftmeta\",stage=\"post_apply_read\"} 0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestMetricsRecordCriticalSignalOutcomes(t *testing.T) {
	resetForTest(t)

	RecordWriteLatency(OutcomeSuccess, 12*time.Millisecond)
	RecordWriteLatency("tenant/document/backend-key", -time.Second)
	RecordVerification(VerificationOutcomeMatch)
	RecordVerification(VerificationOutcomeError)
	RecordVerification("tenant/document/backend-key")
	RecordBackendProbe(BackendOperationHead, OutcomeNotFound)
	IncrementRaftQueueDepth()
	IncrementRaftQueueDepth()
	IncrementRaftQueueDepth()
	DecrementRaftQueueDepth()
	SetRaftQueueDepth(-1)
	SetBlockAppendQueueDepth(4)
	SetBlockAppendQueueDepth(-1)
	RecordBlockSyncBatchSize(4)
	RecordBlockSyncBatchSize(-1)
	RecordBlockSyncLatency(OutcomeSuccess, 3*time.Millisecond)
	RecordBlockSyncLatency("tenant/document/backend-key", -time.Second)
	RecordMetadataCommandBatchSize(3)
	RecordMetadataCommandBatchSize(-1)
	RecordMetadataCommandSyncLatency(OutcomeSuccess, 4*time.Millisecond)
	RecordMetadataCommandSyncLatency("tenant/document/backend-key", -time.Second)
	RecordBatchFailure(BatchComponentBlockstore, BatchStageSync)
	RecordBatchFailure(BatchComponentRaftMeta, BatchStageSync)
	RecordBatchFailure(BatchComponentRaftMeta, BatchStagePostApplyRead)
	families, err := GatherMetrics()
	testutil.RequireNoErrorf(t, err, "gather metrics")
	testutil.RequireTruef(t, len(families) > 0, "gathered metrics are empty")

	body := scrapeMetrics(t)
	for _, want := range []string{
		"scrap_write_latency_seconds_count{outcome=\"success\"} 1",
		"scrap_write_latency_seconds_count{outcome=\"error\"} 1",
		"scrap_verification_total{outcome=\"match\"} 1",
		"scrap_verification_total{outcome=\"mismatch\"} 1",
		"scrap_verification_total{outcome=\"error\"} 1",
		"scrap_backend_probe_total{operation=\"head\",outcome=\"not_found\"} 1",
		"scrap_raft_queue_depth 0",
		"scrap_block_append_queue_depth 0",
		"scrap_block_sync_batch_size_count 2",
		"scrap_block_sync_batch_size_sum 4",
		"scrap_block_sync_latency_seconds_count{outcome=\"success\"} 1",
		"scrap_block_sync_latency_seconds_count{outcome=\"error\"} 1",
		"scrap_metadata_command_batch_size_count 2",
		"scrap_metadata_command_batch_size_sum 3",
		"scrap_metadata_command_sync_latency_seconds_count{outcome=\"success\"} 1",
		"scrap_metadata_command_sync_latency_seconds_count{outcome=\"error\"} 1",
		"scrap_batch_failures_total{component=\"blockstore\",stage=\"sync\"} 1",
		"scrap_batch_failures_total{component=\"raftmeta\",stage=\"sync\"} 1",
		"scrap_batch_failures_total{component=\"raftmeta\",stage=\"post_apply_read\"} 1",
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
	RecordBatchFailure("tenant/component", "tenant/stage")
	body := scrapeMetrics(t)
	if strings.Contains(body, "tenant/document/backend-key") {
		t.Fatalf("metrics body exposed high-cardinality operation label:\n%s", body)
	}
	if strings.Contains(body, "tenant/component") || strings.Contains(body, "tenant/stage") {
		t.Fatalf("metrics body exposed high-cardinality batch labels:\n%s", body)
	}
	if !strings.Contains(body, "scrap_batch_failures_total{component=\"unknown\",stage=\"unknown\"} 1") {
		t.Fatalf("metrics body missing unknown batch labels:\n%s", body)
	}
}
