package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func observeOptions(attrs []attribute.KeyValue) []metric.ObserveOption {
	if len(attrs) == 0 {
		return nil
	}
	return []metric.ObserveOption{metric.WithAttributes(attrs...)}
}
