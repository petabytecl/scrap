package shard_test

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
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

	m.RecordUpload(1, "success", 150*time.Millisecond)
	m.RecordUpload(1, "transient", 200*time.Millisecond)

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

	m.RecordVerify(1, "pass")
	m.RecordVerify(1, "fail")

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
	m.RecordUpload(7, "transient", time.Second)
	m.RecordUpload(7, "tx-raw-status", time.Second)
	m.RecordVerify(7, "pass")
	m.RecordVerify(7, "doc-raw-status")
	m.SetPressureLevel(7, shard.UploadPressureLevelPressure)
	m.SetConcurrency(7, 4)
	m.SetAuthPaused(7, false)

	rm := collectMetrics(t, reader)
	assertUploadMetricsBounded(t, rm, map[string]uploadMetricExpectation{
		"scrap.upload.pending_bytes":  {keys: []string{"scrap.shard_id"}},
		"scrap.upload.pending_blocks": {keys: []string{"scrap.shard_id"}},
		"scrap.upload.total":          {keys: []string{"scrap.shard_id", "status"}, allowedValues: map[string]map[string]struct{}{"status": stringSet("success", "throttled", "transient", "auth", "not_found", "conflict", "corrupt", "permanent", "unknown")}},
		"scrap.upload.duration":       {keys: []string{"scrap.shard_id"}},
		"scrap.upload.verify_total":   {keys: []string{"scrap.shard_id", "status"}, allowedValues: map[string]map[string]struct{}{"status": stringSet("pass", "fail", "unknown")}},
		"scrap.upload.pressure_level": {keys: []string{"scrap.shard_id"}},
		"scrap.upload.concurrency":    {keys: []string{"scrap.shard_id"}},
		"scrap.upload.auth_paused":    {keys: []string{"scrap.shard_id"}},
	})
}

func TestUploadOTelMetrics_ImplementsInterface(t *testing.T) {
	provider, _ := newTestMeter(t)
	m, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	var _ shard.UploadMetrics = m
}

type uploadMetricExpectation struct {
	keys          []string
	allowedValues map[string]map[string]struct{}
}

func assertUploadMetricsBounded(t *testing.T, rm metricdata.ResourceMetrics, expectations map[string]uploadMetricExpectation) {
	t.Helper()
	found := make(map[string]struct{}, len(expectations))
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			metric := &sm.Metrics[i]
			if !strings.HasPrefix(metric.Name, "scrap.upload.") {
				continue
			}
			expectation, ok := expectations[metric.Name]
			if !ok {
				t.Fatalf("metric %s is missing bounded attribute expectations", metric.Name)
			}
			found[metric.Name] = struct{}{}
			assertUploadMetricAttributes(t, metric, expectation)
		}
	}
	for name := range expectations {
		if _, ok := found[name]; !ok {
			t.Fatalf("metric %s not found", name)
		}
	}
}

func assertUploadMetricAttributes(t *testing.T, metric *metricdata.Metrics, expectation uploadMetricExpectation) {
	t.Helper()
	sets := metricAttributeSets(metric)
	if len(sets) == 0 {
		t.Fatalf("metric %s has no attribute sets", metric.Name)
	}
	for _, attrs := range sets {
		assertUploadMetricAttributeSet(t, metric.Name, attrs, expectation)
	}
}

func assertUploadMetricAttributeSet(t *testing.T, metricName string, attrs []attribute.KeyValue, expectation uploadMetricExpectation) {
	t.Helper()
	wantSet := stringSet(expectation.keys...)
	gotSet := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		key := string(attr.Key)
		gotSet[key] = struct{}{}
		if _, ok := wantSet[key]; !ok {
			t.Fatalf("metric %s attribute key %q is not bounded; want only %v", metricName, key, expectation.keys)
		}
		assertUploadMetricAttributeValue(t, metricName, attr, expectation)
	}
	assertUploadMetricRequiredKeys(t, metricName, gotSet, wantSet)
}

func assertUploadMetricAttributeValue(t *testing.T, metricName string, attr attribute.KeyValue, expectation uploadMetricExpectation) {
	t.Helper()
	key := string(attr.Key)
	allowed, ok := expectation.allowedValues[key]
	if !ok {
		return
	}
	value := attr.Value.AsString()
	if _, ok := allowed[value]; !ok {
		t.Fatalf("metric %s attribute %q value %q is not bounded", metricName, key, value)
	}
}

func assertUploadMetricRequiredKeys(t *testing.T, metricName string, gotSet, wantSet map[string]struct{}) {
	t.Helper()
	for key := range wantSet {
		if _, ok := gotSet[key]; !ok {
			t.Fatalf("metric %s missing attribute key %q", metricName, key)
		}
	}
}

func stringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
