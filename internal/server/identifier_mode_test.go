package server_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
	"github.com/petabytecl/scrap/internal/telemetry"
)

// recordingTelemetry captures the span attributes the server attaches to an RPC,
// so a test can assert which identifier form (hashed vs raw) reached the span.
type recordingTelemetry struct {
	mu    sync.Mutex
	attrs []attribute.KeyValue
}

func (r *recordingTelemetry) StartRPC(ctx context.Context, _ string) (context.Context, server.ActiveRPC) {
	return ctx, &recordingRPC{parent: r}
}

func (r *recordingTelemetry) keys() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := make(map[string]string, len(r.attrs))
	for _, a := range r.attrs {
		m[string(a.Key)] = a.Value.AsString()
	}
	return m
}

type recordingRPC struct{ parent *recordingTelemetry }

func (r *recordingRPC) AddSpanAttributes(attrs ...attribute.KeyValue) {
	r.parent.mu.Lock()
	defer r.parent.mu.Unlock()
	r.parent.attrs = append(r.parent.attrs, attrs...)
}

func (r *recordingRPC) Finish(grpccodes.Code) {}

func startServerWith(t *testing.T, store storeapi.Store, opts ...server.Option) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, store, opts...)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func TestServerHashesIdentifiersByDefault(t *testing.T) {
	rec := &recordingTelemetry{}
	client := startServerWith(t, &notLeaderStore{leaderAddr: "leader:9090"}, server.WithTelemetry(rec))

	// HeadDocument attaches identity attributes before touching the store, so the
	// not-leader error does not prevent the assertion.
	_, _ = client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-1",
		DocumentName:  "secret.pdf",
	})

	keys := rec.keys()
	if _, ok := keys["scrap.transaction_id"]; ok {
		t.Fatalf("raw transaction_id leaked with default (fail-closed) mode: %v", keys)
	}
	if _, ok := keys["scrap.document_name"]; ok {
		t.Fatalf("raw document_name leaked with default (fail-closed) mode: %v", keys)
	}
	if _, ok := keys["scrap.transaction.hash"]; !ok {
		t.Fatalf("expected hashed transaction identity by default: %v", keys)
	}
}

func TestServerEmitsRawIdentifiersWhenConfigured(t *testing.T) {
	rec := &recordingTelemetry{}
	client := startServerWith(t, &notLeaderStore{leaderAddr: "leader:9090"},
		server.WithTelemetry(rec),
		server.WithIdentifierMode(telemetry.RawIdentifiersForLocalDebug),
	)

	_, _ = client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-1",
		DocumentName:  "secret.pdf",
	})

	keys := rec.keys()
	if keys["scrap.transaction_id"] != "tx-1" {
		t.Fatalf("expected raw transaction_id in local-debug mode: %v", keys)
	}
	if keys["scrap.document_name"] != "secret.pdf" {
		t.Fatalf("expected raw document_name in local-debug mode: %v", keys)
	}
	if _, ok := keys["scrap.transaction.hash"]; ok {
		t.Fatalf("hashed identity present in raw mode: %v", keys)
	}
}
