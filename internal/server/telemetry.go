package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"

	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/telemetry"
)

const documentServiceName = "scrap.v1.DocumentService"

// Option configures the Document gRPC server.
type Option func(*documentServer)

// Telemetry records Document gRPC server telemetry.
type Telemetry interface {
	StartRPC(ctx context.Context, method string) (context.Context, ActiveRPC)
}

// ActiveRPC records attributes and completion state for one in-flight RPC.
type ActiveRPC interface {
	AddSpanAttributes(attrs ...attribute.KeyValue)
	Finish(code grpccodes.Code)
}

// WithTelemetry records Document gRPC server telemetry through tel.
func WithTelemetry(tel Telemetry) Option {
	return func(s *documentServer) {
		if tel != nil {
			s.telemetry = tel
		}
	}
}

// WithIdentifierMode sets how Document identifiers are attached to span
// attributes. When omitted, the server is fail-closed to hashed identifiers (the
// zero value of telemetry.IdentifierMode). See ADR 0013 §4.
func WithIdentifierMode(mode telemetry.IdentifierMode) Option {
	return func(s *documentServer) {
		s.identifierMode = mode
	}
}

// WithLogger enables structured operational logs for server-side control-flow
// events. The caller is expected to attach bounded Cell/Member/Shard identity to
// the logger before passing it here.
func WithLogger(logger *slog.Logger) Option {
	return func(s *documentServer) {
		if logger != nil {
			s.logger = logger.With("component", "server")
		}
	}
}

// WithAuthorizer enables role authorization for Document RPC handlers.
func WithAuthorizer(authorizer *security.Authorizer) Option {
	return func(s *documentServer) {
		s.authorizer = authorizer
	}
}

type noopTelemetry struct{}

func (noopTelemetry) StartRPC(ctx context.Context, _ string) (context.Context, ActiveRPC) {
	return ctx, noopRPC{}
}

type noopRPC struct{}

func (noopRPC) AddSpanAttributes(...attribute.KeyValue) {}

func (noopRPC) Finish(grpccodes.Code) {}

// OTelTelemetry records Document gRPC server telemetry with OpenTelemetry.
type OTelTelemetry struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
	inFlight metric.Int64UpDownCounter
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

	inFlight, err := meter.Int64UpDownCounter(
		"scrap.rpc.server.in_flight",
		metric.WithDescription("Document service RPCs currently in flight (concurrency utilization)."),
	)
	if err != nil {
		return nil, fmt.Errorf("create RPC in-flight counter: %w", err)
	}

	return &OTelTelemetry{
		requests: requests,
		duration: duration,
		inFlight: inFlight,
		tracer:   tracer,
		now:      time.Now,
	}, nil
}

// StartRPC starts a server span and returns the active RPC recorder.
func (t *OTelTelemetry) StartRPC(ctx context.Context, method string) (context.Context, ActiveRPC) {
	start := t.now()
	ctx, span := t.tracer.Start(ctx, documentServiceName+"/"+method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(rpcSpanAttributes(method)...),
	)
	t.inFlight.Add(ctx, 1, metric.WithAttributes(attribute.String("rpc.method", method)))

	return ctx, &otelRPC{
		telemetry: t,
		ctx:       ctx,
		method:    method,
		span:      span,
		start:     start,
	}
}

type otelRPC struct {
	telemetry *OTelTelemetry
	ctx       context.Context
	method    string
	span      trace.Span
	start     time.Time
}

func (r *otelRPC) AddSpanAttributes(attrs ...attribute.KeyValue) {
	r.span.SetAttributes(attrs...)
}

func (r *otelRPC) Finish(code grpccodes.Code) {
	r.telemetry.inFlight.Add(r.ctx, -1, metric.WithAttributes(attribute.String("rpc.method", r.method)))
	attrs := rpcAttributes(r.method, code)
	r.telemetry.requests.Add(r.ctx, 1, metric.WithAttributes(attrs...))
	r.telemetry.duration.Record(r.ctx, r.telemetry.now().Sub(r.start).Seconds(), metric.WithAttributes(attrs...))
	r.span.SetAttributes(attribute.Int("rpc.grpc.status_code", int(code)))
	if code == grpccodes.OK {
		r.span.SetStatus(otelcodes.Ok, "")
	} else {
		r.span.SetStatus(otelcodes.Error, code.String())
	}
	r.span.End()
}

func rpcAttributes(method string, code grpccodes.Code) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("rpc.service", documentServiceName),
		attribute.String("rpc.method", method),
		attribute.Int("rpc.grpc.status_code", int(code)),
	}
}

func rpcSpanAttributes(method string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("rpc.service", documentServiceName),
		attribute.String("rpc.method", method),
	}
}
