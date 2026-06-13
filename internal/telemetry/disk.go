package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DiskStats is a point-in-time view of a Shard's local storage footprint.
type DiskStats struct {
	UsedBytes       int64
	FreeBytes       int64
	ProjectionBytes int64
}

// DiskStatsProvider supplies local-storage gauges. Local disk is the constraint the
// upload budget exists to protect (CONTEXT.md), so it anchors the USE dashboard.
type DiskStatsProvider interface {
	DiskStats() DiskStats
}

// DiskMetrics registers observable gauges for local disk and projection usage.
type DiskMetrics struct {
	registration metric.Registration
}

// NewDiskMetrics registers scrap.disk.used_bytes, scrap.disk.free_bytes, and
// scrap.pebble.disk_bytes. Names already carry the _bytes suffix and set no unit, so
// the OTel->Prometheus exporter does not append a second suffix.
func NewDiskMetrics(meter metric.Meter, provider DiskStatsProvider, attrs ...attribute.KeyValue) (*DiskMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	if provider == nil {
		return nil, errors.New("disk stats provider is required")
	}
	attrs = append([]attribute.KeyValue(nil), attrs...)

	used, err := meter.Int64ObservableGauge("scrap.disk.used_bytes",
		metric.WithDescription("Used bytes on the local data volume."),
	)
	if err != nil {
		return nil, fmt.Errorf("create disk used_bytes gauge: %w", err)
	}

	free, err := meter.Int64ObservableGauge("scrap.disk.free_bytes",
		metric.WithDescription("Free bytes on the local data volume."),
	)
	if err != nil {
		return nil, fmt.Errorf("create disk free_bytes gauge: %w", err)
	}

	projection, err := meter.Int64ObservableGauge("scrap.pebble.disk_bytes",
		metric.WithDescription("On-disk size of the Pebble projection."),
	)
	if err != nil {
		return nil, fmt.Errorf("create pebble disk_bytes gauge: %w", err)
	}

	reg, err := meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			stats := provider.DiskStats()
			opts := observeOptions(attrs)
			o.ObserveInt64(used, stats.UsedBytes, opts...)
			o.ObserveInt64(free, stats.FreeBytes, opts...)
			o.ObserveInt64(projection, stats.ProjectionBytes, opts...)
			return nil
		},
		used, free, projection,
	)
	if err != nil {
		return nil, fmt.Errorf("register disk callback: %w", err)
	}

	return &DiskMetrics{registration: reg}, nil
}

func (d *DiskMetrics) Unregister() error {
	if d == nil || d.registration == nil {
		return nil
	}
	if err := d.registration.Unregister(); err != nil {
		return fmt.Errorf("unregister disk metrics: %w", err)
	}
	return nil
}
