package server_test

import (
	"context"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type restoreUnavailableStore struct{}

func (restoreUnavailableStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, storeapi.ErrInvalidArgument
}

func (restoreUnavailableStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (restoreUnavailableStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, storeapi.NewUnavailable(storeapi.UnavailableReasonBackendRestoreUnavailable, "Backend restore unavailable")
}

func (restoreUnavailableStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

func TestReadDocumentRestoreUnavailableReturnsErrorInfoDetail(t *testing.T) {
	client := startRestoreUnavailableServer(t)
	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		TransactionId: "tx-restore",
		DocumentName:  "doc.bin",
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected restore unavailable error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("code = %s, want UNAVAILABLE", st.Code())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("reason = %q, want backend_restore_unavailable", info.GetReason())
	}
}

func startRestoreUnavailableServer(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, restoreUnavailableStore{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}
