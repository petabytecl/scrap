package shard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type WriteStageRecorder interface {
	StartStage(ctx context.Context, stage string) (context.Context, WriteStageEnd)
}

type WriteStageEnd interface {
	End(err error)
}

type WriteTelemetry struct {
	stageDuration metric.Float64Histogram
	tracer        trace.Tracer
}

func NewWriteTelemetry(meter metric.Meter, tracer trace.Tracer) (*WriteTelemetry, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	if tracer == nil {
		return nil, errors.New("tracer is required")
	}

	stageDuration, err := meter.Float64Histogram("scrap.write.stage.duration",
		metric.WithDescription("Duration of individual write-path stages."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create write stage duration histogram: %w", err)
	}

	return &WriteTelemetry{
		stageDuration: stageDuration,
		tracer:        tracer,
	}, nil
}

func (wt *WriteTelemetry) StartStage(ctx context.Context, stage string) (context.Context, WriteStageEnd) {
	ctx, span := wt.tracer.Start(ctx, "scrap.write/"+stage,
		trace.WithAttributes(attribute.String("scrap.write.stage", stage)),
	)
	return ctx, &writeStage{
		telemetry: wt,
		span:      span,
		stage:     stage,
		start:     time.Now(),
	}
}

type writeStage struct {
	telemetry *WriteTelemetry
	span      trace.Span
	stage     string
	start     time.Time
}

func (ws *writeStage) End(err error) {
	elapsed := time.Since(ws.start).Seconds()
	status := "ok"
	if err != nil {
		status = "error"
		ws.span.SetStatus(otelcodes.Error, err.Error())
		ws.span.RecordError(err)
	} else {
		ws.span.SetStatus(otelcodes.Ok, "")
	}

	ws.telemetry.stageDuration.Record(context.Background(), elapsed,
		metric.WithAttributes(
			attribute.String("scrap.write.stage", ws.stage),
			attribute.String("status", status),
		),
	)
	ws.span.End()
}

type noopWriteTelemetry struct{}

func (noopWriteTelemetry) StartStage(ctx context.Context, _ string) (context.Context, WriteStageEnd) {
	return ctx, noopStageEnd{}
}

type noopStageEnd struct{}

func (noopStageEnd) End(error) {}
