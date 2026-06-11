package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestWriteDocumentSendsAckOnlyAfterStoreSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body := []byte("payload")
	sum := sha256.Sum256(body)
	createdAt := time.Unix(1, 0).UTC()
	store := &ackGateStore{
		started: make(chan struct{}),
		release: make(chan struct{}),
		result: storeapi.WriteResult{
			SHA256:    sum,
			Size:      int64(len(body)),
			CreatedAt: createdAt,
		},
	}
	client := startServerWith(t, store)

	done := make(chan writeAckResult, 1)
	go func() {
		resp, err := writeAckDocument(ctx, client, "tx-ack", "doc.xml", "text/xml", body)
		done <- writeAckResult{resp: resp, err: err}
	}()

	waitForAckGateSignal(t, store.started, "store write started")
	select {
	case result := <-done:
		t.Fatalf("WriteDocument returned before store success: resp=%+v err=%v", result.resp, result.err)
	default:
	}

	close(store.release)

	result := waitForWriteAckResult(t, done)
	if result.err != nil {
		t.Fatalf("WriteDocument: %v", result.err)
	}
	if result.resp.GetSha256Checksum() != hex.EncodeToString(sum[:]) {
		t.Fatalf("Sha256Checksum = %q, want %q", result.resp.GetSha256Checksum(), hex.EncodeToString(sum[:]))
	}
	if result.resp.GetSize() != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", result.resp.GetSize(), len(body))
	}
	if !result.resp.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want %s", result.resp.GetCreatedAt().AsTime(), createdAt)
	}
	if !bytes.Equal(store.Body(), body) {
		t.Fatalf("store body = %q, want %q", string(store.Body()), string(body))
	}
}

func TestWriteDocumentDoesNotAckStoreError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store := &ackGateStore{
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonUploadPressure, "upload pressure"),
	}
	client := startServerWith(t, store)

	done := make(chan writeAckResult, 1)
	go func() {
		resp, err := writeAckDocument(ctx, client, "tx-ack", "doc.xml", "text/xml", []byte("payload"))
		done <- writeAckResult{resp: resp, err: err}
	}()

	waitForAckGateSignal(t, store.started, "store write started")
	close(store.release)

	result := waitForWriteAckResult(t, done)
	if status.Code(result.err) != codes.ResourceExhausted {
		t.Fatalf("WriteDocument status = %s, want %s (err=%v)", status.Code(result.err), codes.ResourceExhausted, result.err)
	}
	if result.resp != nil {
		t.Fatalf("WriteDocument returned success response for failed store write: %+v", result.resp)
	}
}

func TestWriteDocumentAckEvidenceRedactsRawIdentifiers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		txID    = "tx-raw-redaction"
		docName = "invoice-secret.xml"
	)
	body := []byte("classified-payload")
	sum := sha256.Sum256(body)
	release := make(chan struct{})
	close(release)
	store := &ackGateStore{
		started: make(chan struct{}),
		release: release,
		result: storeapi.WriteResult{
			SHA256:    sum,
			Size:      int64(len(body)),
			CreatedAt: time.Unix(2, 0).UTC(),
		},
	}

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
	client := startServerWith(t, store, server.WithTelemetry(tel), server.WithLogger(logger))
	if _, err := writeAckDocument(ctx, client, txID, docName, "text/xml", body); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	span := spanByName(t, spanRecorder.Ended(), "scrap.v1.DocumentService/WriteDocument")
	attrs := spanAttrMap(span.Attributes())
	assertSpanAttrNotContaining(t, attrs, "scrap.transaction.hash", txID)
	assertSpanAttrNotContaining(t, attrs, "scrap.document.hash", docName)
	assertSpanAttrAbsent(t, attrs, "scrap.transaction_id")
	assertSpanAttrAbsent(t, attrs, "scrap.document_name")
	assertAttributeValuesOmit(t, span.Attributes(), txID, docName, string(body))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	requests := int64DataPoint(t, rm, "scrap.rpc.server.requests")
	assertNoMetricAttr(t, requests.Attributes, "transaction_id")
	assertNoMetricAttr(t, requests.Attributes, "document_name")
	assertMetricValuesOmit(t, requests.Attributes, txID, docName, string(body))
	assertStringOmits(t, logs.String(), txID, docName, string(body))
}

type ackGateStore struct {
	started chan struct{}
	release chan struct{}
	result  storeapi.WriteResult
	err     error
	body    []byte
}

func (s *ackGateStore) WriteDocument(
	ctx context.Context,
	_, _, _, _ string,
	body io.Reader,
) (storeapi.WriteResult, error) {
	data, err := io.ReadAll(body)
	s.body = append([]byte(nil), data...)
	close(s.started)
	if err != nil {
		return storeapi.WriteResult{}, fmt.Errorf("read body: %w", err)
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return storeapi.WriteResult{}, ctx.Err()
	}
	if s.err != nil {
		return storeapi.WriteResult{}, s.err
	}
	return s.result, nil
}

func (s *ackGateStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s *ackGateStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s *ackGateStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

func (s *ackGateStore) Body() []byte {
	return append([]byte(nil), s.body...)
}

type writeAckResult struct {
	resp *scrapv1.WriteDocumentResponse
	err  error
}

func writeAckDocument(
	ctx context.Context,
	client scrapv1.DocumentServiceClient,
	txID string,
	docName string,
	contentType string,
	body []byte,
) (*scrapv1.WriteDocumentResponse, error) {
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return nil, fmt.Errorf("open WriteDocument stream: %w", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			TransactionId: txID,
			DocumentName:  docName,
			ContentType:   contentType,
		}},
	}); err != nil {
		return nil, fmt.Errorf("send init: %w", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: body},
	}); err != nil {
		return nil, fmt.Errorf("send body: %w", err)
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func waitForAckGateSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForWriteAckResult(t *testing.T, done <-chan writeAckResult) writeAckResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for WriteDocument result")
		return writeAckResult{}
	}
}

func assertAttributeValuesOmit(t *testing.T, attrs []attribute.KeyValue, forbidden ...string) {
	t.Helper()
	for _, attr := range attrs {
		assertStringOmits(t, attr.Value.String(), forbidden...)
	}
}

func assertMetricValuesOmit(t *testing.T, attrs attribute.Set, forbidden ...string) {
	t.Helper()
	for _, attr := range attrs.ToSlice() {
		assertStringOmits(t, attr.Value.String(), forbidden...)
	}
}

func assertStringOmits(t *testing.T, got string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(got, value) {
			t.Fatalf("value %q contains forbidden evidence %q", got, value)
		}
	}
}
