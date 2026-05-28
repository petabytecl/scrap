package shard_test

import (
	"context"
	"errors"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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
