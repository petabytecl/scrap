package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"testing"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/localstorage"
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

func TestServerServesLocalStorageApplications(t *testing.T) {
	app, err := localstorage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open local storage: %v", err)
	}
	defer app.Close()

	publicListener := bufconn.Listen(1024 * 1024)
	adminListener := bufconn.Listen(1024 * 1024)
	server := newServer(publicListener, adminListener, Applications{
		Documents:    app,
		Transactions: app,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	publicConn := dialTestServer(t, publicListener)
	defer publicConn.Close()
	documents := scrapv1.NewDocumentServiceClient(publicConn)
	transactions := scrapv1.NewTransactionServiceClient(publicConn)

	data := []byte("invoice bytes")
	sum := sha256.Sum256(data)
	expectedLength := uint64(len(data))
	write, err := documents.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if err := write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
			Identity: &scrapv1.DocumentIdentity{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  "invoice.xml",
			},
			DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
			ExpectedLength:       &expectedLength,
			ExpectedSha256:       sum[:],
			ClientIdempotencyKey: stringPtr("write-1"),
			CreatedByService:     "billing-etl",
		}},
	}); err != nil {
		t.Fatalf("send init: %v", err)
	}
	if err := write.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: data}},
	}); err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	writeResp, err := write.CloseAndRecv()
	if err != nil {
		t.Fatalf("close write: %v", err)
	}
	if writeResp.GetAchievedReplicaCount() != 1 || writeResp.GetMetadata().GetLength() != expectedLength {
		t.Fatalf("write response = %#v, want local 1/1 write metadata", writeResp)
	}

	headResp, err := documents.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: writeResp.GetMetadata().GetIdentity(),
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if headResp.GetMetadata().GetLength() != expectedLength {
		t.Fatalf("head length = %d, want %d", headResp.GetMetadata().GetLength(), expectedLength)
	}

	read, err := documents.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		Identity: writeResp.GetMetadata().GetIdentity(),
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	first, err := read.Recv()
	if err != nil {
		t.Fatalf("recv metadata: %v", err)
	}
	if first.GetMetadata() == nil || first.GetMetadata().GetSource() != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("first read response = %#v, want local metadata", first)
	}
	var got bytes.Buffer
	for {
		msg, err := read.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv chunk: %v", err)
		}
		got.Write(msg.GetChunk().GetData())
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("read bytes = %q, want %q", got.Bytes(), data)
	}

	transactionResp, err := transactions.GetTransaction(context.Background(), &scrapv1.GetTransactionRequest{
		Transaction: &scrapv1.TransactionIdentity{TenantId: "tenant", TransactionId: "txn"},
	})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if transactionResp.GetTransaction().GetDocumentCount() != 1 {
		t.Fatalf("transaction = %#v, want one document", transactionResp.GetTransaction())
	}

	_ = publicConn.Close()
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

func stringPtr(value string) *string {
	return &value
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
