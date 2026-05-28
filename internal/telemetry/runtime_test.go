package telemetry_test

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/telemetry"
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

func TestRuntimeMetrics_NilMeter(t *testing.T) {
	_, err := telemetry.NewRuntimeMetrics(nil)
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestRuntimeMetrics_ObservesGoRuntime(t *testing.T) {
	provider, reader := newTestMeter(t)
	rm, err := telemetry.NewRuntimeMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new runtime metrics: %v", err)
	}
	defer func() { _ = rm.Unregister() }()

	metrics := collectMetrics(t, reader)

	expected := []string{
		"process.runtime.go.goroutines",
		"process.runtime.go.mem.heap_alloc",
		"process.runtime.go.mem.heap_sys",
		"process.runtime.go.gc.count",
		"process.runtime.go.gc.pause_total",
	}

	for _, name := range expected {
		if findMetric(metrics, name) == nil {
			t.Errorf("metric %q not found", name)
		}
	}
}

func TestRuntimeMetrics_GoroutineCountPositive(t *testing.T) {
	provider, reader := newTestMeter(t)
	rm, err := telemetry.NewRuntimeMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new runtime metrics: %v", err)
	}
	defer func() { _ = rm.Unregister() }()

	metrics := collectMetrics(t, reader)
	goroutines := findMetric(metrics, "process.runtime.go.goroutines")
	if goroutines == nil {
		t.Fatal("process.runtime.go.goroutines not found")
	}

	gauge, ok := goroutines.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("expected Gauge[int64], got %T", goroutines.Data)
	}
	if len(gauge.DataPoints) == 0 {
		t.Fatal("no data points")
	}
	if gauge.DataPoints[0].Value <= 0 {
		t.Fatalf("goroutine count should be positive, got %d", gauge.DataPoints[0].Value)
	}
}

func TestRuntimeMetrics_Unregister(t *testing.T) {
	provider, _ := newTestMeter(t)
	rm, err := telemetry.NewRuntimeMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new runtime metrics: %v", err)
	}
	if err := rm.Unregister(); err != nil {
		t.Fatalf("unregister: %v", err)
	}
}

func TestRuntimeMetrics_NilUnregister(t *testing.T) {
	var rm *telemetry.RuntimeMetrics
	if err := rm.Unregister(); err != nil {
		t.Fatalf("nil unregister: %v", err)
	}
}
