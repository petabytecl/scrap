package server_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestGRPCWriteDocumentExactReplayThroughRegisteredShard(t *testing.T) {
	ctx := context.Background()
	client := startShardDocumentServer(t)
	payload := []byte("grpc exact replay")

	first, err := writeAckDocument(ctx, client, "tx-grpc-replay", "doc.xml", "text/xml", payload)
	if err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	second, err := writeAckDocument(ctx, client, "tx-grpc-replay", "doc.xml", "text/xml", payload)
	if err != nil {
		t.Fatalf("exact replay WriteDocument: %v", err)
	}

	if second.GetSha256Checksum() != first.GetSha256Checksum() {
		t.Fatalf("exact replay checksum = %q, want %q", second.GetSha256Checksum(), first.GetSha256Checksum())
	}
	if second.GetSize() != first.GetSize() {
		t.Fatalf("exact replay size = %d, want %d", second.GetSize(), first.GetSize())
	}
	if !second.GetCreatedAt().AsTime().Equal(first.GetCreatedAt().AsTime()) {
		t.Fatalf("exact replay CreatedAt = %s, want %s", second.GetCreatedAt().AsTime(), first.GetCreatedAt().AsTime())
	}
	assertGRPCFindDocumentCount(ctx, t, client, "tx-grpc-replay", 1)
}

func TestGRPCWriteDocumentConflictReturnsAlreadyExistsThroughRegisteredShard(t *testing.T) {
	ctx := context.Background()
	client := startShardDocumentServer(t)

	if _, err := writeAckDocument(ctx, client, "tx-grpc-conflict", "doc.xml", "text/xml", []byte("original")); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	_, err := writeAckDocument(ctx, client, "tx-grpc-conflict", "doc.xml", "application/xml", []byte("changed"))
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting replay status = %s, want %s (err=%v)", status.Code(err), codes.AlreadyExists, err)
	}

	read := readDocument(ctx, t, client, "tx-grpc-conflict", "doc.xml")
	if !bytes.Equal(read, []byte("original")) {
		t.Fatalf("ReadDocument after conflict = %q, want original", string(read))
	}
	assertGRPCFindDocumentCount(ctx, t, client, "tx-grpc-conflict", 1)
}

func TestGRPCWriteDocumentReplayConflictEvidenceRedactsRawValues(t *testing.T) {
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

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := startShardDocumentServer(t, server.WithTelemetry(tel), server.WithLogger(logger))
	const (
		txID     = "tx-secret-replay"
		docName  = "invoice-secret.xml"
		original = "classified-original"
		changed  = "classified-changed"
	)

	if _, err := writeAckDocument(ctx, client, txID, docName, "text/xml", []byte(original)); err != nil {
		t.Fatalf("first WriteDocument: %v", err)
	}
	if _, err := writeAckDocument(ctx, client, txID, docName, "text/xml", []byte(original)); err != nil {
		t.Fatalf("exact replay WriteDocument: %v", err)
	}
	_, err = writeAckDocument(ctx, client, txID, docName, "text/xml", []byte(changed))
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting replay status = %s, want %s (err=%v)", status.Code(err), codes.AlreadyExists, err)
	}
	assertStringOmits(t, err.Error(), txID, docName, original, changed)

	for _, span := range spanRecorder.Ended() {
		assertStringOmits(t, fmt.Sprint(span.Attributes()), txID, docName, original, changed)
		assertStringOmits(t, span.Status().Description, txID, docName, original, changed)
		for _, event := range span.Events() {
			assertStringOmits(t, event.Name, txID, docName, original, changed)
			assertStringOmits(t, fmt.Sprint(event.Attributes), txID, docName, original, changed)
		}
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	assertStringOmits(t, fmt.Sprint(rm), txID, docName, original, changed)
	assertStringOmits(t, logs.String(), txID, docName, original, changed)
}

func startShardDocumentServer(t *testing.T, opts ...server.Option) scrapv1.DocumentServiceClient {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:      t.TempDir(),
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open shard: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	waitForDocumentServerShardLeader(t, s)

	return startServerWith(t, s, opts...)
}

func waitForDocumentServerShardLeader(t *testing.T, s *shard.Shard) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
}

func assertGRPCFindDocumentCount(ctx context.Context, t *testing.T, client scrapv1.DocumentServiceClient, txID string, want int) {
	t.Helper()
	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{TransactionId: txID})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != want {
		t.Fatalf("FindDocuments count = %d, want %d", len(resp.GetDocuments()), want)
	}
}
