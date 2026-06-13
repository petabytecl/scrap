package shard_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/petabytecl/scrap/internal/shard"
)

func TestWriteTelemetry_NilMeter(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(t.Context()) }()

	_, err := shard.NewWriteTelemetry(nil, tp.Tracer("test"))
	if err == nil {
		t.Fatal("expected error for nil meter")
	}
}

func TestWriteTelemetry_NilTracer(t *testing.T) {
	provider, _ := newTestMeter(t)
	_, err := shard.NewWriteTelemetry(provider.Meter("test"), nil)
	if err == nil {
		t.Fatal("expected error for nil tracer")
	}
}

func TestWriteTelemetry_StageCreatesSpanAndMetric(t *testing.T) {
	provider, reader := newTestMeter(t)
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	defer func() { _ = tp.Shutdown(t.Context()) }()

	wt, err := shard.NewWriteTelemetry(provider.Meter("test"), tp.Tracer("test"))
	if err != nil {
		t.Fatalf("new write telemetry: %v", err)
	}

	ctx := context.Background()
	_, stage := wt.StartStage(ctx, "block_append")
	stage.End(nil)

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name() != "scrap.write/block_append" {
		t.Fatalf("span name: got %q, want %q", spans[0].Name(), "scrap.write/block_append")
	}

	rm := collectMetrics(t, reader)
	duration := findMetric(rm, "scrap.write.stage.duration")
	if duration == nil {
		t.Fatal("scrap.write.stage.duration not found")
	}
}

func TestWriteTelemetry_StageRecordsError(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = mp.Shutdown(t.Context()) }()

	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	defer func() { _ = tp.Shutdown(t.Context()) }()

	wt, err := shard.NewWriteTelemetry(mp.Meter("test"), tp.Tracer("test"))
	if err != nil {
		t.Fatalf("new write telemetry: %v", err)
	}

	ctx := context.Background()
	_, stage := wt.StartStage(ctx, "raft_propose")
	stage.End(errors.New("proposal failed"))

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	events := spans[0].Events()
	if len(events) == 0 {
		t.Fatal("expected error event on span")
	}
}

func TestWriteTelemetry_ImplementsInterface(t *testing.T) {
	provider, _ := newTestMeter(t)
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(t.Context()) }()

	wt, err := shard.NewWriteTelemetry(provider.Meter("test"), tp.Tracer("test"))
	if err != nil {
		t.Fatalf("new write telemetry: %v", err)
	}
	var _ shard.WriteStageRecorder = wt
}

func TestWriteTelemetry_StartApplyCreatesSpanOnly(t *testing.T) {
	provider, reader := newTestMeter(t)
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	defer func() { _ = tp.Shutdown(t.Context()) }()

	wt, err := shard.NewWriteTelemetry(provider.Meter("test"), tp.Tracer("test"))
	if err != nil {
		t.Fatalf("new write telemetry: %v", err)
	}

	_, apply := wt.StartSpan(context.Background(), "scrap.apply/commit_document", oteltrace.WithAttributes(attribute.Int64("scrap.block_id", 42)))
	apply.End(errors.New("apply failed"))

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "scrap.apply/commit_document" {
		t.Fatalf("span name: got %q, want scrap.apply/commit_document", span.Name())
	}
	if span.Status().Code != otelcodes.Error {
		t.Fatalf("expected error status, got %v", span.Status().Code)
	}
	var blockID int64 = -1
	for _, a := range span.Attributes() {
		if string(a.Key) == "scrap.block_id" {
			blockID = a.Value.AsInt64()
		}
	}
	if blockID != 42 {
		t.Fatalf("scrap.block_id attribute: got %d, want 42", blockID)
	}

	// Apply spans are span-only: they must NOT record the write-stage duration metric.
	rm := collectMetrics(t, reader)
	if findMetric(rm, "scrap.write.stage.duration") != nil {
		t.Fatal("StartApply must not record scrap.write.stage.duration")
	}
}
