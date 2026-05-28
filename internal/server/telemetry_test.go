package server_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/spike"
)

func TestDocumentRPCRecordsOTelMetrics(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })

	tel, err := server.NewOTelTelemetry(
		meterProvider.Meter("scrapd-test"),
		sdktrace.NewTracerProvider().Tracer("scrapd-test"),
	)
	if err != nil {
		t.Fatalf("NewOTelTelemetry: %v", err)
	}

	client := startTelemetryServer(t, server.WithTelemetry(tel))
	_, err = client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-123",
		DocumentName:  "invoice.xml",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("HeadDocument status = %v, want %v", status.Code(err), codes.NotFound)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	requests := int64DataPoint(t, rm, "scrap.rpc.server.requests")
	if requests.Value != 1 {
		t.Fatalf("request count = %d, want 1", requests.Value)
	}
	assertMetricAttr(t, requests.Attributes, "rpc.service", "scrap.v1.DocumentService")
	assertMetricAttr(t, requests.Attributes, "rpc.method", "HeadDocument")
	assertMetricAttr(t, requests.Attributes, "rpc.grpc.status_code", codes.NotFound.String())
	assertNoMetricAttr(t, requests.Attributes, "transaction_id")
	assertNoMetricAttr(t, requests.Attributes, "document_name")

	duration := histogramDataPoint(t, rm, "scrap.rpc.server.duration")
	if duration.Count != 1 {
		t.Fatalf("duration count = %d, want 1", duration.Count)
	}
	assertMetricAttr(t, duration.Attributes, "rpc.service", "scrap.v1.DocumentService")
	assertMetricAttr(t, duration.Attributes, "rpc.method", "HeadDocument")
	assertMetricAttr(t, duration.Attributes, "rpc.grpc.status_code", codes.NotFound.String())
}

func TestDocumentRPCRecordsOTelSpansWithHashedDocumentIdentity(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	tel, err := server.NewOTelTelemetry(
		meterProvider.Meter("scrapd-test"),
		tracerProvider.Tracer("scrapd-test"),
	)
	if err != nil {
		t.Fatalf("NewOTelTelemetry: %v", err)
	}

	client := startTelemetryServer(t, server.WithTelemetry(tel))
	_, err = client.HeadDocument(ctx, &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-123",
		DocumentName:  "invoice.xml",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("HeadDocument status = %v, want %v", status.Code(err), codes.NotFound)
	}

	span := spanByName(t, spanRecorder.Ended(), "scrap.v1.DocumentService/HeadDocument")
	attrs := spanAttrMap(span.Attributes())
	assertSpanAttr(t, attrs, "rpc.service", "scrap.v1.DocumentService")
	assertSpanAttr(t, attrs, "rpc.method", "HeadDocument")
	assertSpanAttr(t, attrs, "rpc.grpc.status_code", codes.NotFound.String())
	assertSpanAttrNotContaining(t, attrs, "scrap.transaction.hash", "tx-123")
	assertSpanAttrNotContaining(t, attrs, "scrap.document.hash", "invoice.xml")
	assertSpanAttrAbsent(t, attrs, "scrap.transaction_id")
	assertSpanAttrAbsent(t, attrs, "scrap.document_name")
}

func TestWriteDocumentRPCSpanUsesHashedDocumentIdentity(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	tel, err := server.NewOTelTelemetry(
		meterProvider.Meter("scrapd-test"),
		tracerProvider.Tracer("scrapd-test"),
	)
	if err != nil {
		t.Fatalf("NewOTelTelemetry: %v", err)
	}

	client := startTelemetryServer(t, server.WithTelemetry(tel))
	writeDocument(ctx, t, client, "tx-write-123", "invoice.xml", "text/xml", []byte("payload"))

	span := spanByName(t, spanRecorder.Ended(), "scrap.v1.DocumentService/WriteDocument")
	attrs := spanAttrMap(span.Attributes())
	assertSpanAttr(t, attrs, "rpc.service", "scrap.v1.DocumentService")
	assertSpanAttr(t, attrs, "rpc.method", "WriteDocument")
	assertSpanAttr(t, attrs, "rpc.grpc.status_code", codes.OK.String())
	assertSpanAttrNotContaining(t, attrs, "scrap.transaction.hash", "tx-write-123")
	assertSpanAttrNotContaining(t, attrs, "scrap.document.hash", "invoice.xml")
	assertSpanAttrAbsent(t, attrs, "scrap.transaction_id")
	assertSpanAttrAbsent(t, attrs, "scrap.document_name")
}

func TestWriteDocumentTelemetryRecordsInvalidArgumentWhenInitMissing(t *testing.T) {
	ctx := context.Background()
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	tel, err := server.NewOTelTelemetry(
		meterProvider.Meter("scrapd-test"),
		tracerProvider.Tracer("scrapd-test"),
	)
	if err != nil {
		t.Fatalf("NewOTelTelemetry: %v", err)
	}

	client := startTelemetryServer(t, server.WithTelemetry(tel))
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	_, err = stream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CloseAndRecv status = %v, want %v", status.Code(err), codes.InvalidArgument)
	}

	span := spanByName(t, spanRecorder.Ended(), "scrap.v1.DocumentService/WriteDocument")
	attrs := spanAttrMap(span.Attributes())
	assertSpanAttr(t, attrs, "rpc.grpc.status_code", codes.InvalidArgument.String())
	assertSpanAttrAbsent(t, attrs, "scrap.transaction_id")
	assertSpanAttrAbsent(t, attrs, "scrap.document_name")
}

func TestWriteDocumentTelemetryRecordsInvalidArgumentForNonInitFirstMessage(t *testing.T) {
	client := startTelemetryServer(t)
	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("payload")},
	}); err != nil {
		t.Fatalf("Send chunk: %v", err)
	}

	_, err = stream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CloseAndRecv status = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestWriteDocumentTelemetryRecordsInvalidArgumentForMissingIdentity(t *testing.T) {
	client := startTelemetryServer(t)
	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{DocumentName: "invoice.xml"},
		},
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}

	_, err = stream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CloseAndRecv status = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestNewOTelTelemetryRequiresMeterAndTracer(t *testing.T) {
	_, err := server.NewOTelTelemetry(nil, sdktrace.NewTracerProvider().Tracer("scrapd-test"))
	if err == nil || !strings.Contains(err.Error(), "meter is required") {
		t.Fatalf("missing meter error = %v", err)
	}

	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })

	_, err = server.NewOTelTelemetry(meterProvider.Meter("scrapd-test"), nil)
	if err == nil || !strings.Contains(err.Error(), "tracer is required") {
		t.Fatalf("missing tracer error = %v", err)
	}
}

func startTelemetryServer(t *testing.T, opts ...server.Option) scrapv1.DocumentServiceClient {
	t.Helper()

	dir := t.TempDir()
	s, err := spike.Open(dir)
	if err != nil {
		t.Fatalf("spike.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, s, opts...)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func int64DataPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.DataPoint[int64] {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s has data type %T, want int64 sum", name, m.Data)
			}
			if len(sum.DataPoints) == 0 {
				t.Fatalf("%s has no data points", name)
			}
			return sum.DataPoints[0]
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.DataPoint[int64]{}
}

func histogramDataPoint(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.HistogramDataPoint[float64] {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s has data type %T, want float64 histogram", name, m.Data)
			}
			if len(hist.DataPoints) == 0 {
				t.Fatalf("%s has no data points", name)
			}
			return hist.DataPoints[0]
		}
	}
	t.Fatalf("metric %s not found", name)
	return metricdata.HistogramDataPoint[float64]{}
}

func assertMetricAttr(t *testing.T, attrs attribute.Set, key, want string) {
	t.Helper()

	got, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("missing metric attribute %q", key)
	}
	if got.AsString() != want {
		t.Fatalf("metric attribute %q = %q, want %q", key, got.AsString(), want)
	}
}

func assertNoMetricAttr(t *testing.T, attrs attribute.Set, key string) {
	t.Helper()

	for _, kv := range attrs.ToSlice() {
		if string(kv.Key) == key {
			t.Fatalf("forbidden metric attribute %q is present", key)
		}
	}
}

func spanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return nil
}

func spanAttrMap(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	attrs := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

func assertSpanAttr(t *testing.T, attrs map[attribute.Key]attribute.Value, key, want string) {
	t.Helper()

	got, ok := attrs[attribute.Key(key)]
	if !ok {
		t.Fatalf("missing span attribute %q", key)
	}
	if got.AsString() != want {
		t.Fatalf("span attribute %q = %q, want %q", key, got.AsString(), want)
	}
}

func assertSpanAttrNotContaining(t *testing.T, attrs map[attribute.Key]attribute.Value, key, forbidden string) {
	t.Helper()

	got, ok := attrs[attribute.Key(key)]
	if !ok {
		t.Fatalf("missing span attribute %q", key)
	}
	if got.AsString() == "" {
		t.Fatalf("span attribute %q is empty", key)
	}
	if strings.Contains(got.AsString(), forbidden) {
		t.Fatalf("span attribute %q exposes raw identifier %q", key, forbidden)
	}
}

func assertSpanAttrAbsent(t *testing.T, attrs map[attribute.Key]attribute.Value, key string) {
	t.Helper()

	if _, ok := attrs[attribute.Key(key)]; ok {
		t.Fatalf("forbidden span attribute %q is present", key)
	}
}
