package shard_test

import (
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/shard"
)

func newTestMeter(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	return provider, reader
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return rm
}

func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func TestUploadOTelMetrics_NilMeter(t *testing.T) {
	_, err := shard.NewUploadOTelMetrics(nil)
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestUploadOTelMetrics_RecordUpload(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	m.RecordUpload(1, "ok", 150*time.Millisecond)
	m.RecordUpload(1, "error", 200*time.Millisecond)

	rm := collectMetrics(t, reader)
	total := findMetric(rm, "scrap.upload.total")
	if total == nil {
		t.Fatal("scrap.upload.total not found")
	}
	sum, ok := total.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", total.Data)
	}
	if len(sum.DataPoints) != 2 {
		t.Fatalf("expected 2 data points, got %d", len(sum.DataPoints))
	}
}

func TestUploadOTelMetrics_SetPending(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	m.SetPending(1, 4096, 2)

	rm := collectMetrics(t, reader)
	bytes := findMetric(rm, "scrap.upload.pending_bytes")
	if bytes == nil {
		t.Fatal("scrap.upload.pending_bytes not found")
	}
	blocks := findMetric(rm, "scrap.upload.pending_blocks")
	if blocks == nil {
		t.Fatal("scrap.upload.pending_blocks not found")
	}
}

func TestUploadOTelMetrics_PressureAndConcurrency(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	m.SetPressureLevel(1, shard.UploadPressureLevelCritical)
	m.SetConcurrency(1, 4)
	m.SetAuthPaused(1, true)

	rm := collectMetrics(t, reader)
	pressure := findMetric(rm, "scrap.upload.pressure_level")
	if pressure == nil {
		t.Fatal("scrap.upload.pressure_level not found")
	}
	concurrency := findMetric(rm, "scrap.upload.concurrency")
	if concurrency == nil {
		t.Fatal("scrap.upload.concurrency not found")
	}
	authPaused := findMetric(rm, "scrap.upload.auth_paused")
	if authPaused == nil {
		t.Fatal("scrap.upload.auth_paused not found")
	}
}

func TestUploadOTelMetrics_VerifyTotal(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	m.RecordVerify(1, "ok")
	m.RecordVerify(1, "mismatch")

	rm := collectMetrics(t, reader)
	verify := findMetric(rm, "scrap.upload.verify_total")
	if verify == nil {
		t.Fatal("scrap.upload.verify_total not found")
	}
}

func TestUploadOTelMetricsUsesBoundedAttributes(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	m.SetPending(7, 4096, 2)
	m.RecordUpload(7, "success", time.Second)
	m.RecordVerify(7, "pass")
	m.SetPressureLevel(7, shard.UploadPressureLevelPressure)
	m.SetConcurrency(7, 4)
	m.SetAuthPaused(7, false)

	rm := collectMetrics(t, reader)
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.pending_bytes"), "scrap.shard_id")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.pending_blocks"), "scrap.shard_id")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.total"), "scrap.shard_id", "status")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.duration"), "scrap.shard_id")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.verify_total"), "scrap.shard_id", "status")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.pressure_level"), "scrap.shard_id")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.concurrency"), "scrap.shard_id")
	assertUploadMetricAttributeKeys(t, findMetric(rm, "scrap.upload.auth_paused"), "scrap.shard_id")
}

func TestUploadOTelMetrics_ImplementsInterface(t *testing.T) {
	provider, _ := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	var _ shard.UploadMetrics = m
}

func assertUploadMetricAttributeKeys(t *testing.T, metric *metricdata.Metrics, want ...string) {
	t.Helper()
	if metric == nil {
		t.Fatal("metric not found")
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, key := range want {
		wantSet[key] = struct{}{}
	}
	sets := metricAttributeSets(metric)
	if len(sets) == 0 {
		t.Fatalf("metric %s has no attribute sets", metric.Name)
	}
	for _, attrs := range sets {
		gotSet := make(map[string]struct{}, len(attrs))
		for _, attr := range attrs {
			key := string(attr.Key)
			gotSet[key] = struct{}{}
			if _, ok := wantSet[key]; !ok {
				t.Fatalf("metric %s attribute key %q is not bounded; want only %v", metric.Name, key, want)
			}
		}
		for key := range wantSet {
			if _, ok := gotSet[key]; !ok {
				t.Fatalf("metric %s missing attribute key %q", metric.Name, key)
			}
		}
	}
}
