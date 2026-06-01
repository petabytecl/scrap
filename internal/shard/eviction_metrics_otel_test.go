package shard_test

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestEvictionOTelMetrics_RecordPlanUsesLowCardinalityReason(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewEvictionOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new eviction otel metrics: %v", err)
	}

	m.RecordPlan(7, eviction.Plan{
		Reason:          eviction.ReasonEvidenceRun,
		Note:            "operator ticket INC-12345 with freeform details",
		Selected:        []eviction.PlanBlock{{BlockID: 1, SizeBytes: 1024}},
		SelectedBytes:   1024,
		CandidateBlocks: 3,
		CandidateBytes:  4096,
		EligibleBlocks:  2,
		EligibleBytes:   2048,
		SkipCountsByReason: map[string]int{
			eviction.SkipReasonHotResidencyWindow: 1,
		},
	})

	rm := collectMetrics(t, reader)
	plans := findMetric(rm, "scrap.eviction.plans")
	if plans == nil {
		t.Fatal("scrap.eviction.plans not found")
	}
	if !metricHasAttribute(plans, "reason", eviction.ReasonEvidenceRun) {
		t.Fatalf("scrap.eviction.plans missing reason=%s", eviction.ReasonEvidenceRun)
	}
	if metricContainsAttributeKeyOrValue(plans, "note", "INC-12345") {
		t.Fatal("freeform audit note leaked into eviction plan metric attributes")
	}

	skips := findMetric(rm, "scrap.eviction.skips")
	if skips == nil {
		t.Fatal("scrap.eviction.skips not found")
	}
	if !metricHasAttribute(skips, "reason", eviction.SkipReasonHotResidencyWindow) {
		t.Fatalf("scrap.eviction.skips missing reason=%s", eviction.SkipReasonHotResidencyWindow)
	}
}

func TestEvictionOTelMetrics_RecordApplyAndRestore(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewEvictionOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new eviction otel metrics: %v", err)
	}

	m.RecordApply(7, eviction.ReasonEvidenceRun, eviction.ApplyStatusCompleted, 250*time.Millisecond, nil)
	m.RecordRestore(7, shard.RestoreReasonRead, "failed", eviction.RestoreFailureBackendUnavailable, 500*time.Millisecond)

	rm := collectMetrics(t, reader)
	if findMetric(rm, "scrap.eviction.apply.total") == nil {
		t.Fatal("scrap.eviction.apply.total not found")
	}
	restore := findMetric(rm, "scrap.eviction.restore.total")
	if restore == nil {
		t.Fatal("scrap.eviction.restore.total not found")
	}
	if !metricHasAttribute(restore, "failure_reason", eviction.RestoreFailureBackendUnavailable) {
		t.Fatalf("restore metric missing failure_reason=%s", eviction.RestoreFailureBackendUnavailable)
	}
}

func TestEvictionOTelMetrics_RecordApplySkipReasons(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewEvictionOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new eviction otel metrics: %v", err)
	}

	m.RecordApply(7, eviction.ReasonEvidenceRun, eviction.ApplyStatusCompletedWithSkips, 250*time.Millisecond, map[string]int{
		eviction.SkipReasonLocalStateNotHot: 2,
	})

	rm := collectMetrics(t, reader)
	skips := findMetric(rm, "scrap.eviction.skips")
	if skips == nil {
		t.Fatal("scrap.eviction.skips not found")
	}
	if !metricHasAttribute(skips, "reason", eviction.SkipReasonLocalStateNotHot) {
		t.Fatalf("scrap.eviction.skips missing reason=%s", eviction.SkipReasonLocalStateNotHot)
	}
}

func TestEvictionOTelMetrics_SetHealthClearsStaleRestoreFailureReasons(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := shard.NewEvictionOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new eviction otel metrics: %v", err)
	}

	m.SetHealth(7, eviction.HealthSnapshot{
		RestoreFailedBlocks: 1,
		RestoreFailuresByReason: map[string]int{
			eviction.RestoreFailureBackendUnavailable: 1,
		},
	})
	m.SetHealth(7, eviction.HealthSnapshot{})

	rm := collectMetrics(t, reader)
	failures := findMetric(rm, "scrap.eviction.restore_failures_by_reason")
	if failures == nil {
		t.Fatal("scrap.eviction.restore_failures_by_reason not found")
	}
	value, ok := int64GaugeValue(failures, "reason", eviction.RestoreFailureBackendUnavailable)
	if !ok {
		t.Fatalf("restore failure gauge missing reason=%s", eviction.RestoreFailureBackendUnavailable)
	}
	if value != 0 {
		t.Fatalf("restore failure gauge = %d, want stale reason cleared to 0", value)
	}
}

func metricHasAttribute(metric *metricdata.Metrics, key, value string) bool {
	for _, attrs := range metricAttributeSets(metric) {
		for _, attr := range attrs {
			if string(attr.Key) == key && attr.Value.AsString() == value {
				return true
			}
		}
	}
	return false
}

func metricContainsAttributeKeyOrValue(metric *metricdata.Metrics, keyPart, valuePart string) bool {
	for _, attrs := range metricAttributeSets(metric) {
		for _, attr := range attrs {
			if strings.Contains(string(attr.Key), keyPart) || strings.Contains(attr.Value.AsString(), valuePart) {
				return true
			}
		}
	}
	return false
}

func metricAttributeSets(metric *metricdata.Metrics) [][]attribute.KeyValue {
	switch data := metric.Data.(type) {
	case metricdata.Sum[int64]:
		out := make([][]attribute.KeyValue, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			attrs := point.Attributes
			out = append(out, attrs.ToSlice())
		}
		return out
	case metricdata.Histogram[float64]:
		out := make([][]attribute.KeyValue, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			attrs := point.Attributes
			out = append(out, attrs.ToSlice())
		}
		return out
	case metricdata.Gauge[int64]:
		out := make([][]attribute.KeyValue, 0, len(data.DataPoints))
		for _, point := range data.DataPoints {
			attrs := point.Attributes
			out = append(out, attrs.ToSlice())
		}
		return out
	default:
		return nil
	}
}

func int64GaugeValue(metric *metricdata.Metrics, key, value string) (int64, bool) {
	data, ok := metric.Data.(metricdata.Gauge[int64])
	if !ok {
		return 0, false
	}
	for _, point := range data.DataPoints {
		for _, attr := range point.Attributes.ToSlice() {
			if string(attr.Key) == key && attr.Value.AsString() == value {
				return point.Value, true
			}
		}
	}
	return 0, false
}
