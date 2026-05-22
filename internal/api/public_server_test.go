package api

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestPublicServerWriteDocumentStreamsValidatedChunks(t *testing.T) {
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClients(t, documents, &fakeTransactions{})
	defer cleanup()

	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	init := validWriteInit()
	init.Identity.DocumentName = `billing\invoice.xml`
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: init},
	}); err != nil {
		t.Fatalf("send init: %v", err)
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: []byte("abc")}},
	}); err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}

	documents.mu.Lock()
	defer documents.mu.Unlock()
	if documents.writeInit.Identity.DocumentName != "billing/invoice.xml" {
		t.Fatalf("document name = %q, want normalized", documents.writeInit.Identity.DocumentName)
	}
	if got := string(documents.writeChunks[0]); got != "abc" {
		t.Fatalf("chunk = %q, want abc", got)
	}
	if response.GetMetadata().GetIdentity().GetDocumentName() != "billing/invoice.xml" {
		t.Fatalf("response identity was not mapped from internal metadata: %#v", response.GetMetadata().GetIdentity())
	}
}

func TestPublicServerWriteDocumentRejectsBadStreamOrder(t *testing.T) {
	client, _, cleanup := newPublicTestClients(t, &fakeDocuments{}, &fakeTransactions{})
	defer cleanup()

	stream, err := client.WriteDocument(context.Background())
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	err = stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: []byte("abc")}},
	})
	if err == nil {
		_, err = stream.CloseAndRecv()
	}
	violations := requireBadRequest(t, err)
	requireViolation(t, violations, "message", reasonInvalidStreamOrder)
}

func TestPublicServerHeadDocumentValidatesAndMapsMetadata(t *testing.T) {
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClients(t, documents, &fakeTransactions{})
	defer cleanup()

	response, err := client.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      "tenant",
			TransactionId: "txn",
			DocumentName:  `folder\invoice.xml`,
		},
	})
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}

	documents.mu.Lock()
	defer documents.mu.Unlock()
	if documents.headReq.Identity.DocumentName != "folder/invoice.xml" {
		t.Fatalf("head document name = %q, want normalized", documents.headReq.Identity.DocumentName)
	}
	if response.GetMetadata().GetLength() != 3 {
		t.Fatalf("length = %d, want 3", response.GetMetadata().GetLength())
	}
	if response.GetMetadata().ContentType == nil || response.GetMetadata().GetContentType() != "application/pdf" {
		t.Fatalf("content_type was not mapped as present: %#v", response.GetMetadata())
	}
}

func TestPublicServerReadDocumentStreamsMetadataThenBytes(t *testing.T) {
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClients(t, documents, &fakeTransactions{})
	defer cleanup()

	stream, err := client.ReadDocument(context.Background(), &scrapv1.ReadDocumentRequest{
		Identity: validDocumentIdentity(),
	})
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv metadata: %v", err)
	}
	if first.GetMetadata() == nil {
		t.Fatalf("first response = %#v, want metadata", first)
	}
	second, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv chunk: %v", err)
	}
	if got := string(second.GetChunk().GetData()); got != "abc" {
		t.Fatalf("chunk = %q, want abc", got)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("final recv error = %v, want EOF", err)
	}
}

func TestPublicServerCompleteTransactionValidatesAndMapsState(t *testing.T) {
	_, transactions, cleanup := newPublicTestClients(t, &fakeDocuments{}, &fakeTransactions{})
	defer cleanup()

	response, err := transactions.CompleteTransaction(context.Background(), &scrapv1.CompleteTransactionRequest{
		Transaction: validTransactionIdentity(),
	})
	if err != nil {
		t.Fatalf("CompleteTransaction: %v", err)
	}
	if response.GetTransaction().GetState() != scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED {
		t.Fatalf("state = %s, want completed", response.GetTransaction().GetState())
	}
}

func newPublicTestClients(
	t *testing.T,
	documents DocumentApplication,
	transactions TransactionApplication,
) (scrapv1.DocumentServiceClient, scrapv1.TransactionServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterPublicServer(server, NewPublicServer(documents, transactions))
	go func() {
		_ = server.Serve(listener)
	}()

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
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return scrapv1.NewDocumentServiceClient(conn), scrapv1.NewTransactionServiceClient(conn), cleanup
}

type fakeDocuments struct {
	mu          sync.Mutex
	writeInit   WriteDocumentInit
	writeChunks [][]byte
	headReq     HeadDocumentRequest
}

func (f *fakeDocuments) WriteDocument(_ context.Context, init WriteDocumentInit, chunks ChunkReader) (WriteDocumentResult, error) {
	var readChunks [][]byte
	for {
		chunk, err := chunks.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return WriteDocumentResult{}, err
		}
		readChunks = append(readChunks, chunk)
	}

	f.mu.Lock()
	f.writeInit = init
	f.writeChunks = readChunks
	f.mu.Unlock()
	return WriteDocumentResult{
		Metadata:             sampleDocumentMetadata(init.Identity),
		DesiredReplicaCount:  3,
		AchievedReplicaCount: 3,
	}, nil
}

func (f *fakeDocuments) HeadDocument(_ context.Context, req HeadDocumentRequest) (DocumentMetadata, error) {
	f.mu.Lock()
	f.headReq = req
	f.mu.Unlock()
	return sampleDocumentMetadata(req.Identity), nil
}

func (f *fakeDocuments) ReadDocument(_ context.Context, req ReadDocumentRequest, sender ReadDocumentSender) error {
	if err := sender.SendMetadata(ReadDocumentMetadata{
		Metadata:      sampleDocumentMetadata(req.Identity),
		SelectedRange: ReadRange{Offset: 0},
		Source:        scrapv1.StorageSource_STORAGE_SOURCE_LOCAL,
	}); err != nil {
		return err
	}
	return sender.SendChunk([]byte("abc"))
}

func (f *fakeDocuments) FindDocuments(_ context.Context, req FindDocumentsRequest) (FindDocumentsResult, error) {
	return FindDocumentsResult{
		Documents: []DocumentMetadata{
			sampleDocumentMetadata(identity.Document{
				TenantID:      req.Transaction.TenantID,
				TransactionID: req.Transaction.TransactionID,
				DocumentName:  "invoice.xml",
			}),
		},
	}, nil
}

type fakeTransactions struct{}

func (f *fakeTransactions) CompleteTransaction(_ context.Context, req CompleteTransactionRequest) (TransactionState, error) {
	now := time.Unix(20, 0).UTC()
	return TransactionState{
		Transaction:            req.Transaction,
		State:                  scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED,
		DocumentCount:          1,
		PermanentDocumentCount: 1,
		CreatedAt:              time.Unix(10, 0).UTC(),
		CompletedAt:            &now,
	}, nil
}

func (f *fakeTransactions) GetTransaction(_ context.Context, req GetTransactionRequest) (TransactionState, error) {
	return TransactionState{
		Transaction: req.Transaction,
		State:       scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_OPEN,
		CreatedAt:   time.Unix(10, 0).UTC(),
	}, nil
}

func sampleDocumentMetadata(doc identity.Document) DocumentMetadata {
	return DocumentMetadata{
		Identity:                    doc,
		DocumentClass:               scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:               scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ContentType:                 "application/pdf",
		HasContentType:              true,
		Length:                      3,
		LogicalSHA256:               make([]byte, 32),
		DocumentIdentityFingerprint: make([]byte, 16),
		CreatedByService:            "billing-etl",
		CreatedAt:                   time.Unix(10, 0).UTC(),
		FinalizedAt:                 time.Unix(20, 0).UTC(),
		Availability:                scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT,
		LifecycleState:              scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE,
		Tags: map[string]string{
			"workflow": "billing",
		},
	}
}
