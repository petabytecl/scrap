package avscan

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newAvscanTestMeter(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	return provider, reader
}

func collectAvscanMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return rm
}

func TestOTelMetricsNilMeter(t *testing.T) {
	_, err := NewOTelMetrics(nil)
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestOTelMetricsInstrumentsAndBoundedAttributes(t *testing.T) {
	provider, reader := newAvscanTestMeter(t)
	m, err := NewOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new avscan otel metrics: %v", err)
	}

	m.RecordRun(7, string(StatusIdle), string(ReasonNone), 150*time.Millisecond)
	m.RecordRun(7, "tx-raw-status", "rule-private-signature", time.Second)
	m.RecordBlock(7, string(ResultClean), string(ReasonNone))
	m.RecordBlock(7, "document-name-raw", "scanner-error:/tmp/sensitive")
	m.RecordFailure(7, string(ReasonEngineUnavailable))
	m.SetLag(7, 3)
	m.SetInFlight(7, 1)
	m.RecordDuplicate(7)

	rm := collectAvscanMetrics(t, reader)
	assertAvscanMetricsBounded(t, rm, map[string]avscanMetricExpectation{
		"scrap.avscan.runs":                {keys: []string{"scrap.shard_id", "status", "reason"}, allowedValues: avscanRunAllowedValues()},
		"scrap.avscan.run.duration":        {keys: []string{"scrap.shard_id", "status", "reason"}, allowedValues: avscanRunAllowedValues()},
		"scrap.avscan.blocks":              {keys: []string{"scrap.shard_id", "status", "reason"}, allowedValues: avscanBlockAllowedValues()},
		"scrap.avscan.failures":            {keys: []string{"scrap.shard_id", "reason"}, allowedValues: avscanReasonAllowedValues()},
		"scrap.avscan.engine_unavailable":  {keys: []string{"scrap.shard_id"}},
		"scrap.avscan.lag_blocks":          {keys: []string{"scrap.shard_id"}},
		"scrap.avscan.in_flight_blocks":    {keys: []string{"scrap.shard_id"}},
		"scrap.avscan.duplicate_schedules": {keys: []string{"scrap.shard_id"}},
	})
}

func TestOTelMetricsImplementsInterface(t *testing.T) {
	provider, _ := newAvscanTestMeter(t)
	m, err := NewOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new avscan otel metrics: %v", err)
	}
	var _ Metrics = m
}

type avscanMetricExpectation struct {
	keys          []string
	allowedValues map[string]map[string]struct{}
}

func avscanRunAllowedValues() map[string]map[string]struct{} {
	out := avscanReasonAllowedValues()
	out["status"] = avscanStringSet(string(StatusIdle), string(StatusScanning), string(StatusDegraded), "unknown")
	return out
}

func avscanBlockAllowedValues() map[string]map[string]struct{} {
	out := avscanReasonAllowedValues()
	out["status"] = avscanStringSet(string(ResultClean), string(ResultDetected), "failed", "unknown")
	return out
}

func avscanReasonAllowedValues() map[string]map[string]struct{} {
	return map[string]map[string]struct{}{
		"reason": avscanStringSet(
			string(ReasonNone),
			string(ReasonNotLeader),
			string(ReasonListFailed),
			string(ReasonEngineUnavailable),
			string(ReasonScanFailed),
			string(ReasonScanPanic),
			string(ReasonCanceled),
			string(ReasonIOBudget),
			string(ReasonPaused),
			"unknown",
		),
	}
}

func assertAvscanMetricsBounded(t *testing.T, rm metricdata.ResourceMetrics, expectations map[string]avscanMetricExpectation) {
	t.Helper()
	found := make(map[string]struct{}, len(expectations))
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			metric := &sm.Metrics[i]
			if !strings.HasPrefix(metric.Name, "scrap.avscan.") {
				continue
			}
			expectation, ok := expectations[metric.Name]
			if !ok {
				t.Fatalf("metric %s is missing bounded attribute expectations", metric.Name)
			}
			found[metric.Name] = struct{}{}
			assertAvscanMetricAttributes(t, metric, expectation)
		}
	}
	for name := range expectations {
		if _, ok := found[name]; !ok {
			t.Fatalf("metric %s not found", name)
		}
	}
}

func assertAvscanMetricAttributes(t *testing.T, metric *metricdata.Metrics, expectation avscanMetricExpectation) {
	t.Helper()
	sets := avscanMetricAttributeSets(metric)
	if len(sets) == 0 {
		t.Fatalf("metric %s has no attribute sets", metric.Name)
	}
	for _, attrs := range sets {
		assertAvscanMetricAttributeSet(t, metric.Name, attrs, expectation)
	}
}

func avscanMetricAttributeSets(metric *metricdata.Metrics) [][]attribute.KeyValue {
	switch data := metric.Data.(type) {
	case metricdata.Sum[int64]:
		return avscanSumAttributes(data.DataPoints)
	case metricdata.Gauge[int64]:
		return avscanGaugeAttributes(data.DataPoints)
	case metricdata.Histogram[float64]:
		return avscanHistogramAttributes(data.DataPoints)
	default:
		return nil
	}
}

func avscanSumAttributes(dataPoints []metricdata.DataPoint[int64]) [][]attribute.KeyValue {
	out := make([][]attribute.KeyValue, 0, len(dataPoints))
	for _, dataPoint := range dataPoints {
		out = append(out, dataPoint.Attributes.ToSlice())
	}
	return out
}

func avscanGaugeAttributes(dataPoints []metricdata.DataPoint[int64]) [][]attribute.KeyValue {
	out := make([][]attribute.KeyValue, 0, len(dataPoints))
	for _, dataPoint := range dataPoints {
		out = append(out, dataPoint.Attributes.ToSlice())
	}
	return out
}

func avscanHistogramAttributes(dataPoints []metricdata.HistogramDataPoint[float64]) [][]attribute.KeyValue {
	out := make([][]attribute.KeyValue, 0, len(dataPoints))
	for _, dataPoint := range dataPoints {
		out = append(out, dataPoint.Attributes.ToSlice())
	}
	return out
}

func assertAvscanMetricAttributeSet(t *testing.T, metricName string, attrs []attribute.KeyValue, expectation avscanMetricExpectation) {
	t.Helper()
	wantSet := avscanStringSet(expectation.keys...)
	gotSet := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		key := string(attr.Key)
		gotSet[key] = struct{}{}
		if _, ok := wantSet[key]; !ok {
			t.Fatalf("metric %s attribute key %q is not bounded; want only %v", metricName, key, expectation.keys)
		}
		assertAvscanMetricAttributeValue(t, metricName, attr, expectation)
	}
	for key := range wantSet {
		if _, ok := gotSet[key]; !ok {
			t.Fatalf("metric %s missing attribute key %q", metricName, key)
		}
	}
}

func assertAvscanMetricAttributeValue(t *testing.T, metricName string, attr attribute.KeyValue, expectation avscanMetricExpectation) {
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

func avscanStringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
