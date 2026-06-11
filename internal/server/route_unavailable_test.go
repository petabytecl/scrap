package server_test

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/server"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

type routeUnavailableStore struct{}

func (routeUnavailableStore) WriteDocument(context.Context, string, string, string, string, io.Reader) (storeapi.WriteResult, error) {
	return storeapi.WriteResult{}, routeUnavailableError()
}

func (routeUnavailableStore) HeadDocument(context.Context, string, string) (storeapi.DocumentMeta, error) {
	return storeapi.DocumentMeta{}, routeUnavailableError()
}

func (routeUnavailableStore) ReadDocument(context.Context, string, string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	return nil, storeapi.DocumentMeta{}, routeUnavailableError()
}

func (routeUnavailableStore) FindDocuments(context.Context, string) ([]storeapi.DocumentMeta, error) {
	return nil, routeUnavailableError()
}

func routeUnavailableError() error {
	return storeapi.NewUnavailable(storeapi.UnavailableReasonShardRouteUnavailable, "Shard route unavailable")
}

func TestHeadDocumentRouteUnavailableReturnsBoundedErrorInfo(t *testing.T) {
	client := startRouteUnavailableServer(t)
	_, err := client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		TransactionId: "tx-secret-route",
		DocumentName:  "invoice-secret.xml",
	})
	if err == nil {
		t.Fatal("HeadDocument succeeded, want route unavailable error")
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
	if info.GetReason() != storeapi.UnavailableReasonShardRouteUnavailable {
		t.Fatalf("reason = %q, want %q", info.GetReason(), storeapi.UnavailableReasonShardRouteUnavailable)
	}
	rendered := err.Error()
	for _, forbidden := range []string{"tx-secret-route", "secret-route", "invoice-secret.xml"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("route unavailable error leaked %q in %q", forbidden, rendered)
		}
	}
}

func startRouteUnavailableServer(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener, context not meaningful
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	gs := grpc.NewServer()
	server.Register(gs, routeUnavailableStore{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return scrapv1.NewDocumentServiceClient(conn)
}
