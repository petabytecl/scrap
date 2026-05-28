package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
)

const documentServiceName = "scrap.v1.DocumentService"

// Option configures the Document gRPC server.
type Option func(*documentServer)

// Telemetry records Document gRPC server telemetry.
type Telemetry interface {
	StartRPC(ctx context.Context, method string, spanAttrs ...attribute.KeyValue) (context.Context, func(grpccodes.Code))
}

// WithTelemetry records Document gRPC server telemetry through tel.
func WithTelemetry(tel Telemetry) Option {
	return func(s *documentServer) {
		if tel != nil {
			s.telemetry = tel
		}
	}
}

type noopTelemetry struct{}

func (noopTelemetry) StartRPC(ctx context.Context, _ string, _ ...attribute.KeyValue) (context.Context, func(grpccodes.Code)) {
	return ctx, func(grpccodes.Code) {}
}

// OTelTelemetry records Document gRPC server telemetry with OpenTelemetry.
type OTelTelemetry struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
	tracer   trace.Tracer
	now      func() time.Time
}

// NewOTelTelemetry creates OpenTelemetry instruments for Document gRPC server telemetry.
func NewOTelTelemetry(meter metric.Meter, tracer trace.Tracer) (*OTelTelemetry, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}
	if tracer == nil {
		return nil, errors.New("tracer is required")
	}

	requests, err := meter.Int64Counter(
		"scrap.rpc.server.requests",
		metric.WithDescription("Total number of Document service RPCs handled by scrapd."),
	)
	if err != nil {
		return nil, fmt.Errorf("create RPC request counter: %w", err)
	}

	duration, err := meter.Float64Histogram(
		"scrap.rpc.server.duration",
		metric.WithDescription("Duration of Document service RPCs handled by scrapd."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create RPC duration histogram: %w", err)
	}

	return &OTelTelemetry{
		requests: requests,
		duration: duration,
		tracer:   tracer,
		now:      time.Now,
	}, nil
}

// StartRPC starts a server span and returns a completion function that records
// the final status code and duration.
func (t *OTelTelemetry) StartRPC(ctx context.Context, method string, spanAttrs ...attribute.KeyValue) (context.Context, func(grpccodes.Code)) {
	start := t.now()
	attrs := append(rpcSpanAttributes(method), spanAttrs...)
	ctx, span := t.tracer.Start(ctx, documentServiceName+"/"+method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)

	return ctx, func(code grpccodes.Code) {
		attrs := rpcAttributes(method, code)
		t.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
		t.duration.Record(ctx, t.now().Sub(start).Seconds(), metric.WithAttributes(attrs...))
		span.SetAttributes(attribute.String("rpc.grpc.status_code", code.String()))
		if code == grpccodes.OK {
			span.SetStatus(otelcodes.Ok, "")
		} else {
			span.SetStatus(otelcodes.Error, code.String())
		}
		span.End()
	}
}

func rpcAttributes(method string, code grpccodes.Code) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("rpc.service", documentServiceName),
		attribute.String("rpc.method", method),
		attribute.String("rpc.grpc.status_code", code.String()),
	}
}

func rpcSpanAttributes(method string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("rpc.service", documentServiceName),
		attribute.String("rpc.method", method),
	}
}
