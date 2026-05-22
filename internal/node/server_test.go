package node

import (
	"context"
	"net"
	"testing"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestServerServesPublicAndAdminAPIs(t *testing.T) {
	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer publicConn.Close()
	publicClient := scrapv1.NewDocumentServiceClient(publicConn)
	_, err := publicClient.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  "invoice.xml",
		},
	})
	requireCode(t, err, codes.Unimplemented)

	adminConn := dialTestServer(t, adminListener)
	defer adminConn.Close()
	adminClient := adminv1.NewRestoreServiceClient(adminConn)
	_, err = adminClient.StartRestore(context.Background(), &adminv1.StartRestoreRequest{
		OperationId:     "018f6d86-7a22-7abc-8def-123456789abc",
		OperationPlanId: "plan-1",
		PlanHash:        "hash-1",
	})
	requireCode(t, err, codes.Unimplemented)

	_ = publicConn.Close()
	_ = adminConn.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
}

func dialTestServer(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	if st.Code() != code {
		t.Fatalf("code = %s, want %s", st.Code(), code)
	}
}
