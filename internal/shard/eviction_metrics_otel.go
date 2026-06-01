package shard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/petabytecl/scrap/internal/eviction"
)

const metricUnknown = "unknown"

type EvictionOTelMetrics struct {
	plansTotal              metric.Int64Counter
	skipsTotal              metric.Int64Counter
	candidateBlocks         metric.Int64Gauge
	candidateBytes          metric.Int64Gauge
	eligibleBlocks          metric.Int64Gauge
	eligibleBytes           metric.Int64Gauge
	selectedBlocks          metric.Int64Gauge
	selectedBytes           metric.Int64Gauge
	applyTotal              metric.Int64Counter
	applyDuration           metric.Float64Histogram
	restoreTotal            metric.Int64Counter
	restoreDuration         metric.Float64Histogram
	evictedBlocks           metric.Int64Gauge
	evictedBytes            metric.Int64Gauge
	hotCleanupNeededBlocks  metric.Int64Gauge
	metadataLossBlocks      metric.Int64Gauge
	unexpectedLossBlocks    metric.Int64Gauge
	quarantinedBlocks       metric.Int64Gauge
	restoreFailedBlocks     metric.Int64Gauge
	restoreFailuresByReason metric.Int64Gauge
}

//nolint:cyclop // Each instrument maps directly to one eviction evidence field.
func NewEvictionOTelMetrics(meter metric.Meter) (*EvictionOTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	plansTotal, err := meter.Int64Counter("scrap.eviction.plans",
		metric.WithDescription("Total local eviction plans created."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction plans counter: %w", err)
	}
	skipsTotal, err := meter.Int64Counter("scrap.eviction.skips",
		metric.WithDescription("Total local eviction candidate or apply skips."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction skips counter: %w", err)
	}
	candidateBlocks, err := meter.Int64Gauge("scrap.eviction.candidate_blocks",
		metric.WithDescription("Candidate Blocks observed in the latest local eviction plan."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction candidate blocks gauge: %w", err)
	}
	candidateBytes, err := meter.Int64Gauge("scrap.eviction.candidate_bytes",
		metric.WithDescription("Candidate bytes observed in the latest local eviction plan."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction candidate bytes gauge: %w", err)
	}
	eligibleBlocks, err := meter.Int64Gauge("scrap.eviction.eligible_blocks",
		metric.WithDescription("Eligible Blocks observed in the latest local eviction plan."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction eligible blocks gauge: %w", err)
	}
	eligibleBytes, err := meter.Int64Gauge("scrap.eviction.eligible_bytes",
		metric.WithDescription("Eligible bytes observed in the latest local eviction plan."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction eligible bytes gauge: %w", err)
	}
	selectedBlocks, err := meter.Int64Gauge("scrap.eviction.selected_blocks",
		metric.WithDescription("Selected Blocks in the latest local eviction plan."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction selected blocks gauge: %w", err)
	}
	selectedBytes, err := meter.Int64Gauge("scrap.eviction.selected_bytes",
		metric.WithDescription("Selected bytes in the latest local eviction plan."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction selected bytes gauge: %w", err)
	}
	applyTotal, err := meter.Int64Counter("scrap.eviction.apply.total",
		metric.WithDescription("Total local eviction apply campaigns."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction apply counter: %w", err)
	}
	applyDuration, err := meter.Float64Histogram("scrap.eviction.apply.duration",
		metric.WithDescription("Duration of local eviction apply campaigns."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction apply duration histogram: %w", err)
	}
	restoreTotal, err := meter.Int64Counter("scrap.eviction.restore.total",
		metric.WithDescription("Total Backend restore attempts for evicted Blocks."),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction restore counter: %w", err)
	}
	restoreDuration, err := meter.Float64Histogram("scrap.eviction.restore.duration",
		metric.WithDescription("Duration of Backend restore attempts for evicted Blocks."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create eviction restore duration histogram: %w", err)
	}
	health, err := newEvictionHealthInstruments(meter)
	if err != nil {
		return nil, err
	}

	return &EvictionOTelMetrics{
		plansTotal:              plansTotal,
		skipsTotal:              skipsTotal,
		candidateBlocks:         candidateBlocks,
		candidateBytes:          candidateBytes,
		eligibleBlocks:          eligibleBlocks,
		eligibleBytes:           eligibleBytes,
		selectedBlocks:          selectedBlocks,
		selectedBytes:           selectedBytes,
		applyTotal:              applyTotal,
		applyDuration:           applyDuration,
		restoreTotal:            restoreTotal,
		restoreDuration:         restoreDuration,
		evictedBlocks:           health.evictedBlocks,
		evictedBytes:            health.evictedBytes,
		hotCleanupNeededBlocks:  health.hotCleanupNeededBlocks,
		metadataLossBlocks:      health.metadataLossBlocks,
		unexpectedLossBlocks:    health.unexpectedLossBlocks,
		quarantinedBlocks:       health.quarantinedBlocks,
		restoreFailedBlocks:     health.restoreFailedBlocks,
		restoreFailuresByReason: health.restoreFailuresByReason,
	}, nil
}

type evictionHealthInstruments struct {
	evictedBlocks           metric.Int64Gauge
	evictedBytes            metric.Int64Gauge
	hotCleanupNeededBlocks  metric.Int64Gauge
	metadataLossBlocks      metric.Int64Gauge
	unexpectedLossBlocks    metric.Int64Gauge
	quarantinedBlocks       metric.Int64Gauge
	restoreFailedBlocks     metric.Int64Gauge
	restoreFailuresByReason metric.Int64Gauge
}

func newEvictionHealthInstruments(meter metric.Meter) (evictionHealthInstruments, error) {
	evictedBlocks, err := meter.Int64Gauge("scrap.eviction.evicted_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create evicted blocks gauge: %w", err)
	}
	evictedBytes, err := meter.Int64Gauge("scrap.eviction.evicted_bytes", metric.WithUnit("By"))
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create evicted bytes gauge: %w", err)
	}
	hotCleanupNeededBlocks, err := meter.Int64Gauge("scrap.eviction.hot_cleanup_needed_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create hot cleanup needed gauge: %w", err)
	}
	metadataLossBlocks, err := meter.Int64Gauge("scrap.eviction.metadata_loss_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create metadata loss gauge: %w", err)
	}
	unexpectedLossBlocks, err := meter.Int64Gauge("scrap.eviction.unexpected_loss_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create unexpected loss gauge: %w", err)
	}
	quarantinedBlocks, err := meter.Int64Gauge("scrap.eviction.quarantined_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create quarantined blocks gauge: %w", err)
	}
	restoreFailedBlocks, err := meter.Int64Gauge("scrap.eviction.restore_failed_blocks")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create restore failed blocks gauge: %w", err)
	}
	restoreFailuresByReason, err := meter.Int64Gauge("scrap.eviction.restore_failures_by_reason")
	if err != nil {
		return evictionHealthInstruments{}, fmt.Errorf("create restore failures by reason gauge: %w", err)
	}
	return evictionHealthInstruments{
		evictedBlocks:           evictedBlocks,
		evictedBytes:            evictedBytes,
		hotCleanupNeededBlocks:  hotCleanupNeededBlocks,
		metadataLossBlocks:      metadataLossBlocks,
		unexpectedLossBlocks:    unexpectedLossBlocks,
		quarantinedBlocks:       quarantinedBlocks,
		restoreFailedBlocks:     restoreFailedBlocks,
		restoreFailuresByReason: restoreFailuresByReason,
	}, nil
}

func (m *EvictionOTelMetrics) RecordPlan(shardID uint64, plan eviction.Plan) {
	ctx := context.Background()
	reason := metricString(plan.Reason)
	attrs := metric.WithAttributes(shardAttribute(shardID), attribute.String("reason", reason))
	m.plansTotal.Add(ctx, 1, attrs)
	m.candidateBlocks.Record(ctx, int64(plan.CandidateBlocks), attrs)
	m.candidateBytes.Record(ctx, plan.CandidateBytes, attrs)
	m.eligibleBlocks.Record(ctx, int64(plan.EligibleBlocks), attrs)
	m.eligibleBytes.Record(ctx, plan.EligibleBytes, attrs)
	m.selectedBlocks.Record(ctx, int64(len(plan.Selected)), attrs)
	m.selectedBytes.Record(ctx, plan.SelectedBytes, attrs)
	for skipReason, count := range plan.SkipCountsByReason {
		m.skipsTotal.Add(ctx, int64(count),
			metric.WithAttributes(shardAttribute(shardID), attribute.String("reason", metricString(skipReason))))
	}
}

func (m *EvictionOTelMetrics) RecordApply(shardID uint64, reason, status string, duration time.Duration) {
	attrs := metric.WithAttributes(
		shardAttribute(shardID),
		attribute.String("reason", metricString(reason)),
		attribute.String("status", metricString(status)),
	)
	m.applyTotal.Add(context.Background(), 1, attrs)
	m.applyDuration.Record(context.Background(), duration.Seconds(), attrs)
}

func (m *EvictionOTelMetrics) RecordRestore(shardID uint64, reason, result, failureReason string, duration time.Duration) {
	attrs := metric.WithAttributes(
		shardAttribute(shardID),
		attribute.String("reason", metricString(reason)),
		attribute.String("result", metricString(result)),
		attribute.String("failure_reason", metricString(failureReason)),
	)
	m.restoreTotal.Add(context.Background(), 1, attrs)
	m.restoreDuration.Record(context.Background(), duration.Seconds(), attrs)
}

func (m *EvictionOTelMetrics) SetHealth(shardID uint64, snapshot eviction.HealthSnapshot) {
	attrs := metric.WithAttributes(shardAttribute(shardID))
	ctx := context.Background()
	m.evictedBlocks.Record(ctx, int64(snapshot.EvictedBlocks), attrs)
	m.evictedBytes.Record(ctx, snapshot.EvictedBytes, attrs)
	m.hotCleanupNeededBlocks.Record(ctx, int64(snapshot.HotCleanupNeededBlocks), attrs)
	m.metadataLossBlocks.Record(ctx, int64(snapshot.MetadataLossBlocks), attrs)
	m.unexpectedLossBlocks.Record(ctx, int64(snapshot.UnexpectedLossBlocks), attrs)
	m.quarantinedBlocks.Record(ctx, int64(snapshot.QuarantinedBlocks), attrs)
	m.restoreFailedBlocks.Record(ctx, int64(snapshot.RestoreFailedBlocks), attrs)
	for reason, count := range snapshot.RestoreFailuresByReason {
		m.restoreFailuresByReason.Record(ctx, int64(count),
			metric.WithAttributes(shardAttribute(shardID), attribute.String("reason", metricString(reason))))
	}
}

func metricString(value string) string {
	if value == "" {
		return metricUnknown
	}
	return value
}
