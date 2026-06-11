package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/server"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestGRPCFindDocumentsReturnsTransactionScopedMetadataInWriteOrder(t *testing.T) {
	ctx := context.Background()
	client, _ := startReadVerificationShardServer(t)

	firstPayload := []byte("first grpc find payload")
	first := writeDocument(ctx, t, client, "tx-find-grpc-scope", "b.xml", "text/xml", firstPayload)
	secondPayload := []byte(`{"second":true}`)
	second := writeDocument(ctx, t, client, "tx-find-grpc-scope", "a.json", "application/json", secondPayload)
	writeDocument(ctx, t, client, "tx-find-grpc-other", "b.xml", "text/xml", []byte("other transaction"))

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-grpc-scope",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 2 {
		t.Fatalf("FindDocuments returned %d Documents, want 2: %+v", len(resp.GetDocuments()), resp.GetDocuments())
	}
	assertGRPCDocumentMetaMatchesWrite(t, resp.GetDocuments()[0], "b.xml", "text/xml", int64(len(firstPayload)), first)
	assertGRPCDocumentMetaMatchesWrite(t, resp.GetDocuments()[1], "a.json", "application/json", int64(len(secondPayload)), second)
}

func TestGRPCFindDocumentsEmptyTransactionReturnsOKEmpty(t *testing.T) {
	client, _ := startReadVerificationShardServer(t)

	resp, err := client.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-grpc-empty",
	})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 0 {
		t.Fatalf("FindDocuments returned %d Documents, want empty list: %+v", len(resp.GetDocuments()), resp.GetDocuments())
	}
}

func TestGRPCFindDocumentsRejectsInvalidLookupBeforeStore(t *testing.T) {
	tests := []struct {
		name string
		req  *scrapv1.FindDocumentsRequest
	}{
		{
			name: "invalid transaction_id",
			req: &scrapv1.FindDocumentsRequest{
				TransactionId: "tx-\ninvalid",
			},
		},
		{
			name: "invalid tenant_id",
			req: &scrapv1.FindDocumentsRequest{
				TransactionId: "tx-valid",
				TenantId:      strings.Repeat("t", storeapi.MaxTenantIDBytes+1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &findDocumentsRecordingStore{}
			client := startServerWith(t, store)

			_, err := client.FindDocuments(context.Background(), tt.req)
			assertStatusCode(t, err, codes.InvalidArgument)
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
		})
	}
}

func TestGRPCFindDocumentsCorruptIndexReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	client, s := startReadVerificationShardServer(t)
	writeDocument(ctx, t, client, "tx-find-grpc-corrupt", "doc.xml", "text/xml", []byte("payload"))

	idxPath := block.IdxFilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	if err := os.WriteFile(idxPath, []byte("bad index"), 0o600); err != nil {
		t.Fatalf("corrupt idx: %v", err)
	}

	_, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-grpc-corrupt",
	})
	assertStatusCode(t, err, codes.DataLoss)
}

func TestGRPCFindDocumentsTelemetryRedactsRawIdentifiers(t *testing.T) {
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

	client, _ := startFindDocumentsShardServer(t, server.WithTelemetry(tel))
	const txID = "tx-find-secret"
	const docName = "invoice-secret.xml"
	body := []byte("classified-payload")
	writeDocument(ctx, t, client, txID, docName, "text/xml", body)

	resp, err := client.FindDocuments(ctx, &scrapv1.FindDocumentsRequest{TransactionId: txID})
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(resp.GetDocuments()) != 1 {
		t.Fatalf("FindDocuments returned %d Documents, want 1: %+v", len(resp.GetDocuments()), resp.GetDocuments())
	}

	span := spanByName(t, spanRecorder.Ended(), "scrap.v1.DocumentService/FindDocuments")
	attrs := spanAttrMap(span.Attributes())
	assertSpanAttr(t, attrs, "rpc.service", "scrap.v1.DocumentService")
	assertSpanAttr(t, attrs, "rpc.method", "FindDocuments")
	assertSpanStatusCode(t, attrs, int64(codes.OK))
	assertSpanAttrNotContaining(t, attrs, "scrap.transaction.hash", txID)
	assertSpanAttrAbsent(t, attrs, "scrap.transaction_id")
	assertSpanAttrAbsent(t, attrs, "scrap.document.hash")
	assertSpanAttrAbsent(t, attrs, "scrap.document_name")
	assertAttributeValuesOmit(t, span.Attributes(), txID, docName, string(body), "local/shards", ".blk", ".idx")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	requests := rpcRequestDataPoint(t, rm, "FindDocuments")
	if requests.Value != 1 {
		t.Fatalf("FindDocuments request count = %d, want 1", requests.Value)
	}
	assertMetricAttr(t, requests.Attributes, "rpc.service", "scrap.v1.DocumentService")
	assertMetricAttr(t, requests.Attributes, "rpc.method", "FindDocuments")
	assertMetricAttrInt(t, requests.Attributes, "rpc.grpc.status_code", int64(codes.OK))
	assertNoMetricAttr(t, requests.Attributes, "transaction_id")
	assertNoMetricAttr(t, requests.Attributes, "document_name")
	assertMetricValuesOmit(t, requests.Attributes, txID, docName, string(body), "local/shards", ".blk", ".idx")
}

func TestGRPCFindDocumentsNotLeaderLogRedactsRequestIdentity(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With(
			"service.instance.id", "scrapd-1",
			"scrap.cell_id", "local",
			"scrap.member_slot_id", "scrapd-1",
			"scrap.member_id", "member-123",
			"scrap.shard_id", "0",
			"scrap.raft_id", "2",
		)

	client := startServerWith(t,
		&notLeaderStore{leaderAddr: "scrapd-0.scrap-headless.scrap.svc.cluster.local:9090"},
		server.WithLogger(logger),
	)
	_, err := client.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		TransactionId: "tx-find-secret",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("FindDocuments status = %v, want Unavailable", status.Code(err))
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", logs.String(), err)
	}

	assertLogField(t, entry, "level", "DEBUG")
	assertLogField(t, entry, "msg", "request redirected to shard leader")
	assertLogField(t, entry, "component", "server")
	assertLogField(t, entry, "rpc.service", "scrap.v1.DocumentService")
	assertLogField(t, entry, "rpc.method", "FindDocuments")
	assertLogField(t, entry, "rpc.grpc.status_code", "Unavailable")
	assertLogField(t, entry, "reason", "not_leader")
	assertLogFieldAbsent(t, entry, "transaction_id")
	assertLogFieldAbsent(t, entry, "document_name")
	assertStringOmits(t, logs.String(), "tx-find-secret", "local/shards", ".blk", ".idx")
}

func startFindDocumentsShardServer(t *testing.T, opts ...server.Option) (scrapv1.DocumentServiceClient, string) {
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
	waitForReadVerificationShardLeader(t, s)

	return startServerWith(t, s, opts...), s.DataDirForTest()
}

type findDocumentsRecordingStore struct {
	calls int
}

func (s *findDocumentsRecordingStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	s.calls++
	return storeapi.WriteResult{}, storeapi.ErrInvalidArgument
}

func (s *findDocumentsRecordingStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	s.calls++
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s *findDocumentsRecordingStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.calls++
	return nil, storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (s *findDocumentsRecordingStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	s.calls++
	return nil, nil
}

func assertGRPCDocumentMetaMatchesWrite(
	t *testing.T,
	meta *scrapv1.DocumentMeta,
	docName string,
	contentType string,
	size int64,
	writeResp *scrapv1.WriteDocumentResponse,
) {
	t.Helper()

	if meta.GetName() != docName {
		t.Fatalf("DocumentMeta Name = %q, want %q", meta.GetName(), docName)
	}
	if meta.GetContentType() != contentType {
		t.Fatalf("DocumentMeta ContentType = %q, want %q", meta.GetContentType(), contentType)
	}
	if meta.GetSize() != size {
		t.Fatalf("DocumentMeta Size = %d, want %d", meta.GetSize(), size)
	}
	if meta.GetSha256Checksum() != writeResp.GetSha256Checksum() {
		t.Fatalf("DocumentMeta SHA = %q, want %q", meta.GetSha256Checksum(), writeResp.GetSha256Checksum())
	}
	if !meta.GetCreatedAt().AsTime().Equal(writeResp.GetCreatedAt().AsTime()) {
		t.Fatalf("DocumentMeta CreatedAt = %v, want %v", meta.GetCreatedAt().AsTime(), writeResp.GetCreatedAt().AsTime())
	}
}

func rpcRequestDataPoint(t *testing.T, rm metricdata.ResourceMetrics, method string) metricdata.DataPoint[int64] {
	t.Helper()

	for _, point := range rpcRequestDataPoints(t, rm) {
		got, ok := point.Attributes.Value(attribute.Key("rpc.method"))
		if ok && got.AsString() == method {
			return point
		}
	}
	t.Fatalf("metric scrap.rpc.server.requests with rpc.method=%q not found", method)
	return metricdata.DataPoint[int64]{}
}

func rpcRequestDataPoints(t *testing.T, rm metricdata.ResourceMetrics) []metricdata.DataPoint[int64] {
	t.Helper()

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "scrap.rpc.server.requests" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s has data type %T, want int64 sum", m.Name, m.Data)
			}
			return sum.DataPoints
		}
	}
	t.Fatal("metric scrap.rpc.server.requests not found")
	return nil
}
