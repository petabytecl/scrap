package telemetry_test

import (
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/petabytecl/scrap/internal/telemetry"
)

type stubDiskStats struct{ stats telemetry.DiskStats }

func (s stubDiskStats) DiskStats() telemetry.DiskStats { return s.stats }

func TestDiskMetrics_NilMeter(t *testing.T) {
	_, err := telemetry.NewDiskMetrics(nil, stubDiskStats{})
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestDiskMetrics_NilProvider(t *testing.T) {
	provider, _ := newTestMeter(t)
	_, err := telemetry.NewDiskMetrics(provider.Meter("test"), nil)
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestDiskMetrics_ObservesStats(t *testing.T) {
	provider, reader := newTestMeter(t)
	dm, err := telemetry.NewDiskMetrics(provider.Meter("test"), stubDiskStats{stats: telemetry.DiskStats{
		UsedBytes:       1024,
		FreeBytes:       2048,
		ProjectionBytes: 512,
	}})
	if err != nil {
		t.Fatalf("new disk metrics: %v", err)
	}
	defer func() { _ = dm.Unregister() }()

	metrics := collectMetrics(t, reader)
	want := map[string]int64{
		"scrap.disk.used_bytes":   1024,
		"scrap.disk.free_bytes":   2048,
		"scrap.pebble.disk_bytes": 512,
	}
	for name, value := range want {
		assertInt64Gauge(t, metrics, name, value)
	}
}

func TestDiskMetrics_NilUnregister(t *testing.T) {
	var dm *telemetry.DiskMetrics
	if err := dm.Unregister(); err != nil {
		t.Fatalf("nil unregister: %v", err)
	}
}

func assertInt64Gauge(t *testing.T, metrics metricdata.ResourceMetrics, name string, want int64) {
	t.Helper()
	m := findMetric(metrics, name)
	if m == nil {
		t.Fatalf("%s not found", name)
	}
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s: expected Gauge[int64], got %T", name, m.Data)
	}
	if len(g.DataPoints) == 0 || g.DataPoints[0].Value != want {
		t.Fatalf("%s = %v, want %d", name, g.DataPoints, want)
	}
}
