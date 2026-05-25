package api

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/petabytecl/scrap/internal/authz"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPublicServerWriteDocumentStreamsValidatedChunks(t *testing.T) {
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClients(t, documents, &fakeTransactions{})
	defer cleanup()

	stream, err := client.WriteDocument(context.Background())
	testutil.RequireNoErrorf(t, err, "WriteDocument")
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
	testutil.RequireNoErrorf(t, err, "CloseAndRecv")

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
	testutil.RequireNoErrorf(t, err, "WriteDocument")
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
	testutil.RequireNoErrorf(t, err, "HeadDocument")

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
	testutil.RequireNoErrorf(t, err, "ReadDocument")
	first, err := stream.Recv()
	testutil.RequireNoErrorf(t, err, "recv metadata")
	if first.GetMetadata() == nil {
		t.Fatalf("first response = %#v, want metadata", first)
	}
	second, err := stream.Recv()
	testutil.RequireNoErrorf(t, err, "recv chunk")
	if got := string(second.GetChunk().GetData()); got != "abc" {
		t.Fatalf("chunk = %q, want abc", got)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("final recv error = %v, want EOF", err)
	}
}

func TestPublicServerFindDocumentsValidatesAndMapsResults(t *testing.T) {
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClients(t, documents, &fakeTransactions{})
	defer cleanup()

	docClass := scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT
	prefix := `folder\`
	response, err := client.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{
		Transaction: validTransactionIdentity(),
		Filter: &scrapv1.DocumentFilter{
			DocumentNamePrefix: &prefix,
			DocumentClass:      &docClass,
		},
	})
	testutil.RequireNoErrorf(t, err, "FindDocuments")
	testutil.RequireEqualf(t, len(response.GetDocuments()), 1, "document count")
	testutil.RequireEqualf(t, response.GetDocuments()[0].GetIdentity().GetDocumentName(), "invoice.xml", "document name")
}

func TestPublicServerCompleteTransactionValidatesAndMapsState(t *testing.T) {
	_, transactions, cleanup := newPublicTestClients(t, &fakeDocuments{}, &fakeTransactions{})
	defer cleanup()

	response, err := transactions.CompleteTransaction(context.Background(), &scrapv1.CompleteTransactionRequest{
		Transaction: validTransactionIdentity(),
	})
	testutil.RequireNoErrorf(t, err, "CompleteTransaction")
	if response.GetTransaction().GetState() != scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED {
		t.Fatalf("state = %s, want completed", response.GetTransaction().GetState())
	}
}

func TestPublicServerGetTransactionValidatesAndMapsState(t *testing.T) {
	_, transactions, cleanup := newPublicTestClients(t, &fakeDocuments{}, &fakeTransactions{})
	defer cleanup()

	response, err := transactions.GetTransaction(context.Background(), &scrapv1.GetTransactionRequest{
		Transaction: validTransactionIdentity(),
	})
	testutil.RequireNoErrorf(t, err, "GetTransaction")
	testutil.RequireEqualf(t, response.GetTransaction().GetState(), scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_OPEN, "transaction state")
	testutil.RequireEqualf(t, response.GetTransaction().GetTransaction().GetTenantId(), "tenant", "transaction tenant")
}

func TestPublicServerRejectsUnconfiguredApplications(t *testing.T) {
	documents, transactions, cleanup := newPublicTestClients(t, nil, nil)
	defer cleanup()

	_, err := documents.HeadDocument(context.Background(), &scrapv1.HeadDocumentRequest{Identity: validDocumentIdentity()})
	requireUnimplementedStatus(t, err)
	_, err = documents.FindDocuments(context.Background(), &scrapv1.FindDocumentsRequest{Transaction: validTransactionIdentity()})
	requireUnimplementedStatus(t, err)
	_, err = transactions.CompleteTransaction(context.Background(), &scrapv1.CompleteTransactionRequest{Transaction: validTransactionIdentity()})
	requireUnimplementedStatus(t, err)
	_, err = transactions.GetTransaction(context.Background(), &scrapv1.GetTransactionRequest{Transaction: validTransactionIdentity()})
	requireUnimplementedStatus(t, err)
}

func requireUnimplementedStatus(t *testing.T, err error) {
	t.Helper()
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("status code = %s, want %s; err = %v", got, codes.Unimplemented, err)
	}
}

func TestPublicServerAuditsCriticalAndEphemeralWritesOnly(t *testing.T) {
	store := openPublicTestOperationStore(t)
	documents := &fakeDocuments{}
	client, _, cleanup := newPublicTestClientsWithAuditStore(t, documents, &fakeTransactions{}, store)
	defer cleanup()

	normal := validWriteInit()
	normal.Identity.DocumentName = "normal.xml"
	err := writeDocumentForAudit(context.Background(), t, client, normal)
	testutil.RequireNoErrorf(t, err, "normal write")
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	testutil.RequireEqualf(t, len(events), 0, "ordinary write audit event count")

	critical := validWriteInit()
	critical.Identity.DocumentName = "critical.xml"
	critical.PriorityClass = scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST
	critical.ClientIdempotencyKey = ptr("critical-write-1")
	critical.Tags["plaintext_secret"] = "do-not-audit"
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		authz.WorkloadIdentityMetadataKey, "billing-etl",
		"x-request-id", "req-critical",
		"x-correlation-id", "corr-critical",
	))
	err = writeDocumentForAudit(ctx, t, client, critical)
	testutil.RequireNoErrorf(t, err, "critical write")

	ephemeral := validWriteInit()
	ephemeral.Identity.DocumentName = "ephemeral.xml"
	ephemeral.DocumentClass = scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL
	err = writeDocumentForAudit(context.Background(), t, client, ephemeral)
	testutil.RequireNoErrorf(t, err, "ephemeral write")

	events, err = store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireCriticalAndEphemeralWriteAuditEvents(t, events)
}

func requireCriticalAndEphemeralWriteAuditEvents(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 2, "critical and ephemeral audit event count")
	byType := make(map[string]*adminv1.AuditEvent, len(events))
	for _, event := range events {
		byType[event.GetEventType()] = event
	}
	criticalEvent := byType["document_write_critical"]
	testutil.RequireEqualf(t, criticalEvent.GetActorIdentity(), "billing-etl", "critical audit actor")
	testutil.RequireEqualf(t, criticalEvent.GetMetadata()["correlation_id"], "corr-critical", "critical audit correlation id")
	testutil.RequireEqualf(t, criticalEvent.GetMetadata()["priority_class"], scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST.String(), "critical audit priority")
	testutil.RequireEqualf(t, criticalEvent.GetMetadata()["plaintext_secret"], "", "critical audit sanitized secret")
	testutil.RequireEqualf(t, len(criticalEvent.GetTargets()), 1, "critical audit target count")
	testutil.RequireEqualf(t, criticalEvent.GetTargets()[0].GetDocument().GetDocumentName(), "critical.xml", "critical audit document")
	ephemeralEvent := byType["document_write_ephemeral"]
	testutil.RequireEqualf(t, ephemeralEvent.GetMetadata()["document_class"], scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL.String(), "ephemeral audit class")
	testutil.RequireEqualf(t, ephemeralEvent.GetActorIdentity(), "billing-etl", "ephemeral audit actor")
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return scrapv1.NewDocumentServiceClient(conn), scrapv1.NewTransactionServiceClient(conn), cleanup
}

func newPublicTestClientsWithAuditStore(
	t *testing.T,
	documents DocumentApplication,
	transactions TransactionApplication,
	store *operations.Store,
) (scrapv1.DocumentServiceClient, scrapv1.TransactionServiceClient, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterPublicServer(server, NewPublicServer(documents, transactions, WithPublicAuditStore(store)))
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
	testutil.RequireNoErrorf(t, err, "grpc.NewClient")
	cleanup := func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
	return scrapv1.NewDocumentServiceClient(conn), scrapv1.NewTransactionServiceClient(conn), cleanup
}

func openPublicTestOperationStore(t *testing.T) *operations.Store {
	t.Helper()
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operation store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close operation store")
	})
	return store
}

func writeDocumentForAudit(
	ctx context.Context,
	t *testing.T,
	client scrapv1.DocumentServiceClient,
	init *scrapv1.WriteDocumentInit,
) error {
	t.Helper()
	stream, err := client.WriteDocument(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Init{Init: init},
	}); err != nil {
		return err
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{
		Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: []byte("abc")}},
	}); err != nil {
		return err
	}
	_, err = stream.CloseAndRecv()
	return err
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
		if errors.Is(err, io.EOF) {
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
		Source:        storageapp.StorageSourceLocal,
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
		State:                  storageapp.TransactionStateCompleted,
		DocumentCount:          1,
		PermanentDocumentCount: 1,
		CreatedAt:              time.Unix(10, 0).UTC(),
		CompletedAt:            &now,
	}, nil
}

func (f *fakeTransactions) GetTransaction(_ context.Context, req GetTransactionRequest) (TransactionState, error) {
	return TransactionState{
		Transaction: req.Transaction,
		State:       storageapp.TransactionStateOpen,
		CreatedAt:   time.Unix(10, 0).UTC(),
	}, nil
}

func sampleDocumentMetadata(doc identity.Document) DocumentMetadata {
	return DocumentMetadata{
		Identity:                    doc,
		DocumentClass:               storageapp.DocumentClassPermanent,
		PriorityClass:               storageapp.PriorityClassNormal,
		ContentType:                 "application/pdf",
		HasContentType:              true,
		Length:                      3,
		LogicalSHA256:               make([]byte, 32),
		DocumentIdentityFingerprint: make([]byte, 16),
		CreatedByService:            "billing-etl",
		CreatedAt:                   time.Unix(10, 0).UTC(),
		FinalizedAt:                 time.Unix(20, 0).UTC(),
		Availability:                storageapp.DocumentAvailabilityHot,
		LifecycleState:              storageapp.DocumentLifecycleStateActive,
		Tags: map[string]string{
			"workflow": "billing",
		},
	}
}
