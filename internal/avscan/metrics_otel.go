package avscan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type OTelMetrics struct {
	runsTotal          metric.Int64Counter
	runDuration        metric.Float64Histogram
	blocksTotal        metric.Int64Counter
	failuresTotal      metric.Int64Counter
	engineUnavailable  metric.Int64Counter
	lagBlocks          metric.Int64Gauge
	inFlightBlocks     metric.Int64Gauge
	duplicateSchedules metric.Int64Counter
}

func NewOTelMetrics(meter metric.Meter) (*OTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	runs, err := meter.Int64Counter("scrap.avscan.runs",
		metric.WithDescription("Total Content Scanner scheduler run outcomes."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan runs counter: %w", err)
	}

	runDuration, err := meter.Float64Histogram("scrap.avscan.run.duration",
		metric.WithDescription("Duration of Content Scanner scheduler runs."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan run duration histogram: %w", err)
	}

	blocks, err := meter.Int64Counter("scrap.avscan.blocks",
		metric.WithDescription("Total Content Scanner Block scan outcomes."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan Blocks counter: %w", err)
	}

	failures, err := meter.Int64Counter("scrap.avscan.failures",
		metric.WithDescription("Total bounded Content Scanner failure reasons."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan failures counter: %w", err)
	}

	engineUnavailable, err := meter.Int64Counter("scrap.avscan.engine_unavailable",
		metric.WithDescription("Total Content Scanner engine unavailable failures."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan engine unavailable counter: %w", err)
	}

	lag, err := meter.Int64Gauge("scrap.avscan.lag_blocks",
		metric.WithDescription("Current sealed Block scanner lag."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan lag gauge: %w", err)
	}

	inFlight, err := meter.Int64Gauge("scrap.avscan.in_flight_blocks",
		metric.WithDescription("Current Content Scanner Blocks in flight."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan in-flight gauge: %w", err)
	}

	duplicates, err := meter.Int64Counter("scrap.avscan.duplicate_schedules",
		metric.WithDescription("Total duplicate Content Scanner schedule skips."),
	)
	if err != nil {
		return nil, fmt.Errorf("create avscan duplicate schedule counter: %w", err)
	}

	return &OTelMetrics{
		runsTotal:          runs,
		runDuration:        runDuration,
		blocksTotal:        blocks,
		failuresTotal:      failures,
		engineUnavailable:  engineUnavailable,
		lagBlocks:          lag,
		inFlightBlocks:     inFlight,
		duplicateSchedules: duplicates,
	}, nil
}

func (m *OTelMetrics) RecordRun(shardID uint64, status, reason string, duration time.Duration) {
	status = boundedRunStatus(status)
	reason = boundedReason(reason)
	attrs := metric.WithAttributes(
		avscanShardAttribute(shardID),
		attribute.String("status", status),
		attribute.String("reason", reason),
	)
	m.runsTotal.Add(context.Background(), 1, attrs)
	m.runDuration.Record(context.Background(), duration.Seconds(), attrs)
}

func (m *OTelMetrics) RecordBlock(shardID uint64, status, reason string) {
	status = boundedBlockStatus(status)
	reason = boundedReason(reason)
	m.blocksTotal.Add(context.Background(), 1, metric.WithAttributes(
		avscanShardAttribute(shardID),
		attribute.String("status", status),
		attribute.String("reason", reason),
	))
}

func (m *OTelMetrics) RecordFailure(shardID uint64, reason string) {
	reason = boundedReason(reason)
	attrs := metric.WithAttributes(avscanShardAttribute(shardID), attribute.String("reason", reason))
	m.failuresTotal.Add(context.Background(), 1, attrs)
	if reason == string(ReasonEngineUnavailable) {
		m.engineUnavailable.Add(context.Background(), 1, metric.WithAttributes(avscanShardAttribute(shardID)))
	}
}

func (m *OTelMetrics) SetLag(shardID uint64, blocks int) {
	m.lagBlocks.Record(context.Background(), int64(blocks), metric.WithAttributes(avscanShardAttribute(shardID)))
}

func (m *OTelMetrics) SetInFlight(shardID uint64, blocks int) {
	m.inFlightBlocks.Record(context.Background(), int64(blocks), metric.WithAttributes(avscanShardAttribute(shardID)))
}

func (m *OTelMetrics) RecordDuplicate(shardID uint64) {
	m.duplicateSchedules.Add(context.Background(), 1, metric.WithAttributes(avscanShardAttribute(shardID)))
}

func avscanShardAttribute(shardID uint64) attribute.KeyValue {
	v := int64(math.MaxInt64)
	if shardID <= math.MaxInt64 {
		v = int64(shardID)
	}
	return attribute.Int64("scrap.shard_id", v)
}

func boundedRunStatus(status string) string {
	switch status {
	case string(StatusIdle), string(StatusScanning), string(StatusDegraded):
		return status
	default:
		return "unknown"
	}
}

func boundedBlockStatus(status string) string {
	switch status {
	case string(ResultClean), string(ResultDetected), "failed":
		return status
	default:
		return "unknown"
	}
}

func boundedReason(reason string) string {
	switch reason {
	case string(ReasonNone),
		string(ReasonNotLeader),
		string(ReasonListFailed),
		string(ReasonEngineUnavailable),
		string(ReasonScanFailed),
		string(ReasonScanPanic),
		string(ReasonCanceled),
		string(ReasonIOBudget),
		string(ReasonPaused),
		string(ReasonProgressFailed),
		string(ReasonQuarantineFailed):
		return reason
	default:
		return "unknown"
	}
}

var _ Metrics = (*OTelMetrics)(nil)
