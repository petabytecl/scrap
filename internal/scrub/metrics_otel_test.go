package scrub_test

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/scrub"
)

func newTestMeter(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	return provider, reader
}

func collectOTelMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return rm
}

func findOTelMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func TestOTelScrubMetrics_NilMeter(t *testing.T) {
	_, err := scrub.NewOTelMetrics(nil)
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestOTelScrubMetrics_RecordRun(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := scrub.NewOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new otel scrub metrics: %v", err)
	}

	m.RecordRun("ok", 1.5)
	m.RecordRun("mismatch", 2.0)

	rm := collectOTelMetrics(t, reader)

	runs := findOTelMetric(rm, "scrap.scrub.light.runs")
	if runs == nil {
		t.Fatal("scrap.scrub.light.runs not found")
	}
	sum, ok := runs.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", runs.Data)
	}
	if len(sum.DataPoints) != 2 {
		t.Fatalf("expected 2 data points (ok, mismatch), got %d", len(sum.DataPoints))
	}

	duration := findOTelMetric(rm, "scrap.scrub.light.duration")
	if duration == nil {
		t.Fatal("scrap.scrub.light.duration not found")
	}
}

func TestOTelScrubMetrics_ImplementsInterface(t *testing.T) {
	provider, _ := newTestMeter(t)
	m, err := scrub.NewOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new otel scrub metrics: %v", err)
	}
	var _ scrub.Metrics = m
}

func TestOTelDeepScrubMetrics_NilMeter(t *testing.T) {
	_, err := scrub.NewOTelDeepMetrics(nil)
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestOTelDeepScrubMetrics_AllInstruments(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := scrub.NewOTelDeepMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new otel deep scrub metrics: %v", err)
	}

	m.RecordDeepRun("ok", 1.5)
	m.RecordFramesVerified(100)
	m.RecordCorruption("frame_crc")
	m.RecordQuarantine()
	m.SetBadDiskSuspected(true)
	m.RecordPause()
	m.SetProgressRatio(0.75)
	m.RecordRepair("ok")
	m.RecordSkip("evicted")
	m.DecrementQuarantined()

	rm := collectOTelMetrics(t, reader)

	expected := []string{
		"scrap.scrub.deep.runs",
		"scrap.scrub.deep.frames_verified",
		"scrap.scrub.deep.corruptions",
		"scrap.scrub.deep.quarantines",
		"scrap.scrub.deep.blocks_quarantined",
		"scrap.scrub.deep.bad_disk_suspected",
		"scrap.scrub.deep.pauses",
		"scrap.scrub.deep.duration",
		"scrap.scrub.deep.repairs",
		"scrap.scrub.deep.skips",
	}

	for _, name := range expected {
		if findOTelMetric(rm, name) == nil {
			t.Errorf("metric %q not found", name)
		}
	}
}

func TestOTelDeepScrubMetrics_ImplementsInterface(t *testing.T) {
	provider, _ := newTestMeter(t)
	m, err := scrub.NewOTelDeepMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new otel deep scrub metrics: %v", err)
	}
	var _ scrub.DeepMetrics = m
}

func TestOTelDeepScrubMetrics_ProgressRatio(t *testing.T) {
	provider, reader := newTestMeter(t)
	m, err := scrub.NewOTelDeepMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new otel deep scrub metrics: %v", err)
	}

	m.SetProgressRatio(0.42)

	rm := collectOTelMetrics(t, reader)
	progress := findOTelMetric(rm, "scrap.scrub.deep.progress_ratio")
	if progress == nil {
		t.Fatal("scrap.scrub.deep.progress_ratio not found")
	}
	gauge, ok := progress.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("expected Gauge[float64], got %T", progress.Data)
	}
	if len(gauge.DataPoints) == 0 {
		t.Fatal("no data points")
	}
	if gauge.DataPoints[0].Value != 0.42 {
		t.Fatalf("progress ratio: got %f, want 0.42", gauge.DataPoints[0].Value)
	}
}
