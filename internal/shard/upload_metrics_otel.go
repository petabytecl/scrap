package shard

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type UploadOTelMetrics struct {
	pendingBytes  metric.Int64Gauge
	pendingBlocks metric.Int64Gauge
	uploadsTotal  metric.Int64Counter
	duration      metric.Float64Histogram
	verifyTotal   metric.Int64Counter
	pressureLevel metric.Int64Gauge
	concurrency   metric.Int64Gauge
	authPaused    metric.Int64Gauge
}

func NewUploadOTelMetrics(meter metric.Meter) (*UploadOTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	pendingBytes, err := meter.Int64Gauge("scrap.upload.pending_bytes",
		metric.WithDescription("Bytes in the Upload Outbox awaiting backend upload."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload pending bytes gauge: %w", err)
	}

	pendingBlocks, err := meter.Int64Gauge("scrap.upload.pending_blocks",
		metric.WithDescription("Blocks in the Upload Outbox awaiting backend upload."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload pending blocks gauge: %w", err)
	}

	uploadsTotal, err := meter.Int64Counter("scrap.upload.total",
		metric.WithDescription("Total backend block upload outcomes."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload total counter: %w", err)
	}

	duration, err := meter.Float64Histogram("scrap.upload.duration",
		metric.WithDescription("Duration of backend block uploads."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload duration histogram: %w", err)
	}

	verifyTotal, err := meter.Int64Counter("scrap.upload.verify_total",
		metric.WithDescription("Total backend upload HEAD verification outcomes."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload verify counter: %w", err)
	}

	pressureLevel, err := meter.Int64Gauge("scrap.upload.pressure_level",
		metric.WithDescription("Current upload pressure level: 0=ok, 1=warn, 2=pressure, 3=critical."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload pressure gauge: %w", err)
	}

	concurrency, err := meter.Int64Gauge("scrap.upload.concurrency",
		metric.WithDescription("Current effective backend upload concurrency."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload concurrency gauge: %w", err)
	}

	authPaused, err := meter.Int64Gauge("scrap.upload.auth_paused",
		metric.WithDescription("Whether backend uploads are paused due to authentication failure."),
	)
	if err != nil {
		return nil, fmt.Errorf("create upload auth paused gauge: %w", err)
	}

	return &UploadOTelMetrics{
		pendingBytes:  pendingBytes,
		pendingBlocks: pendingBlocks,
		uploadsTotal:  uploadsTotal,
		duration:      duration,
		verifyTotal:   verifyTotal,
		pressureLevel: pressureLevel,
		concurrency:   concurrency,
		authPaused:    authPaused,
	}, nil
}

func (m *UploadOTelMetrics) SetPending(shardID uint64, pendingBytes int64, pendingBlocks int) {
	attrs := metric.WithAttributes(shardAttribute(shardID))
	m.pendingBytes.Record(context.Background(), pendingBytes, attrs)
	m.pendingBlocks.Record(context.Background(), int64(pendingBlocks), attrs)
}

func (m *UploadOTelMetrics) RecordUpload(shardID uint64, status string, duration time.Duration) {
	status = boundedUploadMetricStatus(status)
	attrs := metric.WithAttributes(shardAttribute(shardID), attribute.String("status", status))
	m.uploadsTotal.Add(context.Background(), 1, attrs)
	m.duration.Record(context.Background(), duration.Seconds(), metric.WithAttributes(shardAttribute(shardID)))
}

func (m *UploadOTelMetrics) RecordVerify(shardID uint64, status string) {
	status = boundedVerifyMetricStatus(status)
	attrs := metric.WithAttributes(shardAttribute(shardID), attribute.String("status", status))
	m.verifyTotal.Add(context.Background(), 1, attrs)
}

func (m *UploadOTelMetrics) SetPressureLevel(shardID uint64, level UploadPressureLevel) {
	m.pressureLevel.Record(context.Background(), int64(level), metric.WithAttributes(shardAttribute(shardID)))
}

func (m *UploadOTelMetrics) SetConcurrency(shardID uint64, concurrency int) {
	m.concurrency.Record(context.Background(), int64(concurrency), metric.WithAttributes(shardAttribute(shardID)))
}

func (m *UploadOTelMetrics) SetAuthPaused(shardID uint64, paused bool) {
	value := int64(0)
	if paused {
		value = 1
	}
	m.authPaused.Record(context.Background(), value, metric.WithAttributes(shardAttribute(shardID)))
}

func shardAttribute(shardID uint64) attribute.KeyValue {
	v := int64(math.MaxInt64)
	if shardID <= math.MaxInt64 {
		v = int64(shardID)
	}
	return attribute.Int64("scrap.shard_id", v)
}

func boundedUploadMetricStatus(status string) string {
	switch status {
	case "success", "throttled", "transient", "auth", "not_found", "conflict", "corrupt", "permanent", "unknown":
		return status
	default:
		return "unknown"
	}
}

func boundedVerifyMetricStatus(status string) string {
	switch status {
	case "pass", "fail", "unknown":
		return status
	default:
		return "unknown"
	}
}
