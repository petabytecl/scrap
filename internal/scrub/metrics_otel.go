package scrub

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type OTelMetrics struct {
	runsTotal metric.Int64Counter
	duration  metric.Float64Histogram
}

func NewOTelMetrics(meter metric.Meter) (*OTelMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	runs, err := meter.Int64Counter("scrap.scrub.light.runs",
		metric.WithDescription("Total number of light scrub runs."),
	)
	if err != nil {
		return nil, fmt.Errorf("create scrub runs counter: %w", err)
	}

	duration, err := meter.Float64Histogram("scrap.scrub.light.duration",
		metric.WithDescription("Duration of light scrub runs."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create scrub duration histogram: %w", err)
	}

	return &OTelMetrics{
		runsTotal: runs,
		duration:  duration,
	}, nil
}

func (m *OTelMetrics) RecordRun(result string, durationSec float64) {
	ctx := context.Background()
	attrs := metric.WithAttributes(attribute.String("result", result))
	m.runsTotal.Add(ctx, 1, attrs)
	m.duration.Record(ctx, durationSec, attrs)
}

type OTelDeepMetrics struct {
	deepRunsTotal     metric.Int64Counter
	framesVerified    metric.Int64Counter
	corruptionsTotal  metric.Int64Counter
	quarantinesTotal  metric.Int64Counter
	blocksQuarantined metric.Int64UpDownCounter
	progressRatio     metric.Float64Gauge
	badDiskSuspected  metric.Int64Gauge
	pausedTotal       metric.Int64Counter
	deepDuration      metric.Float64Histogram
	repairsTotal      metric.Int64Counter
	skipsTotal        metric.Int64Counter
}

//nolint:cyclop // straight-line constructor registering many independent instruments
func NewOTelDeepMetrics(meter metric.Meter) (*OTelDeepMetrics, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	deepRuns, err := meter.Int64Counter("scrap.scrub.deep.runs",
		metric.WithDescription("Total number of deep scrub runs."),
	)
	if err != nil {
		return nil, fmt.Errorf("create deep scrub runs counter: %w", err)
	}

	frames, err := meter.Int64Counter("scrap.scrub.deep.frames_verified",
		metric.WithDescription("Total number of frames verified by deep scrub."),
	)
	if err != nil {
		return nil, fmt.Errorf("create frames verified counter: %w", err)
	}

	corruptions, err := meter.Int64Counter("scrap.scrub.deep.corruptions",
		metric.WithDescription("Total corruptions detected."),
	)
	if err != nil {
		return nil, fmt.Errorf("create corruptions counter: %w", err)
	}

	quarantines, err := meter.Int64Counter("scrap.scrub.deep.quarantines",
		metric.WithDescription("Total number of blocks quarantined."),
	)
	if err != nil {
		return nil, fmt.Errorf("create quarantines counter: %w", err)
	}

	blocksQ, err := meter.Int64UpDownCounter("scrap.scrub.deep.blocks_quarantined",
		metric.WithDescription("Current number of quarantined blocks."),
	)
	if err != nil {
		return nil, fmt.Errorf("create blocks quarantined counter: %w", err)
	}

	progress, err := meter.Float64Gauge("scrap.scrub.deep.progress_ratio",
		metric.WithDescription("Progress ratio of current deep scrub cycle."),
	)
	if err != nil {
		return nil, fmt.Errorf("create progress ratio gauge: %w", err)
	}

	badDisk, err := meter.Int64Gauge("scrap.scrub.deep.bad_disk_suspected",
		metric.WithDescription("Set to 1 when bad disk is suspected."),
	)
	if err != nil {
		return nil, fmt.Errorf("create bad disk gauge: %w", err)
	}

	paused, err := meter.Int64Counter("scrap.scrub.deep.pauses",
		metric.WithDescription("Total number of deep scrub pauses due to latency backpressure."),
	)
	if err != nil {
		return nil, fmt.Errorf("create pauses counter: %w", err)
	}

	duration, err := meter.Float64Histogram("scrap.scrub.deep.duration",
		metric.WithDescription("Duration of deep scrub runs."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create deep scrub duration histogram: %w", err)
	}

	repairs, err := meter.Int64Counter("scrap.scrub.deep.repairs",
		metric.WithDescription("Total number of block repair attempts."),
	)
	if err != nil {
		return nil, fmt.Errorf("create repairs counter: %w", err)
	}

	skips, err := meter.Int64Counter("scrap.scrub.deep.skips",
		metric.WithDescription("Total number of Blocks skipped by deep scrub."),
	)
	if err != nil {
		return nil, fmt.Errorf("create skips counter: %w", err)
	}

	return &OTelDeepMetrics{
		deepRunsTotal:     deepRuns,
		framesVerified:    frames,
		corruptionsTotal:  corruptions,
		quarantinesTotal:  quarantines,
		blocksQuarantined: blocksQ,
		progressRatio:     progress,
		badDiskSuspected:  badDisk,
		pausedTotal:       paused,
		deepDuration:      duration,
		repairsTotal:      repairs,
		skipsTotal:        skips,
	}, nil
}

func (m *OTelDeepMetrics) RecordDeepRun(result string, durationSec float64) {
	ctx := context.Background()
	attrs := metric.WithAttributes(attribute.String("result", result))
	m.deepRunsTotal.Add(ctx, 1, attrs)
	m.deepDuration.Record(ctx, durationSec, attrs)
}

func (m *OTelDeepMetrics) RecordFramesVerified(n uint64) {
	m.framesVerified.Add(context.Background(), clampFrames(n))
}

func clampFrames(n uint64) int64 {
	if n > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}

func (m *OTelDeepMetrics) RecordCorruption(corruptionType string) {
	m.corruptionsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("type", corruptionType)))
}

func (m *OTelDeepMetrics) RecordQuarantine() {
	ctx := context.Background()
	m.quarantinesTotal.Add(ctx, 1)
	m.blocksQuarantined.Add(ctx, 1)
}

func (m *OTelDeepMetrics) SetBadDiskSuspected(v bool) {
	value := int64(0)
	if v {
		value = 1
	}
	m.badDiskSuspected.Record(context.Background(), value)
}

func (m *OTelDeepMetrics) RecordPause() {
	m.pausedTotal.Add(context.Background(), 1)
}

func (m *OTelDeepMetrics) SetProgressRatio(v float64) {
	m.progressRatio.Record(context.Background(), v)
}

func (m *OTelDeepMetrics) RecordRepair(result string) {
	m.repairsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("result", result)))
}

func (m *OTelDeepMetrics) DecrementQuarantined() {
	m.blocksQuarantined.Add(context.Background(), -1)
}

func (m *OTelDeepMetrics) RecordSkip(reason string) {
	m.skipsTotal.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("reason", reason)))
}
