package server_test

import (
	"context"
	"io"
	"net"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type uploadPressureStore struct{}

func (uploadPressureStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, storeapi.NewResourceExhausted(storeapi.ResourceExhaustedReasonUploadPressure, "upload pressure")
}

func (uploadPressureStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (uploadPressureStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, storeapi.ErrNotFound
}

func (uploadPressureStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, storeapi.ErrNotFound
}

func TestWriteDocumentUploadPressureReturnsErrorInfoDetail(t *testing.T) {
	client := startUploadPressureServer(t)
	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_Init{
			Init: &scrapv1.WriteDocumentInit{
				TransactionId: "tx-pressure",
				DocumentName:  "doc.xml",
				ContentType:   "text/xml",
			},
		},
	}); err != nil {
		t.Fatalf("Send init: %v", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Part: &scrapv1.WriteDocumentRequest_ChunkData{ChunkData: []byte("payload")},
	}); err != nil {
		t.Fatalf("Send chunk: %v", err)
	}

	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("expected upload pressure error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("code = %s, want RESOURCE_EXHAUSTED", st.Code())
	}
	info := errorInfoDetail(st)
	if info == nil {
		t.Fatalf("expected ErrorInfo detail, details=%T", st.Details())
	}
	if info.GetReason() != storeapi.ResourceExhaustedReasonUploadPressure {
		t.Fatalf("reason = %q, want upload_pressure", info.GetReason())
	}
}

func startUploadPressureServer(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, uploadPressureStore{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}

func errorInfoDetail(st *status.Status) *errdetails.ErrorInfo {
	for _, detail := range st.Details() {
		if d, ok := detail.(*errdetails.ErrorInfo); ok {
			return d
		}
	}
	return nil
}
