package localstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/api"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWriteHeadReadFindAndCompleteTransaction(t *testing.T) {
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(100, 0).UTC())
	data := []byte("0123456789")
	sum := sha256.Sum256(data)
	expectedLength := uint64(len(data))
	doc := testDocumentIdentity()

	result, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:             doc,
		DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ContentType:          "application/xml",
		ExpectedLength:       &expectedLength,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: "write-1",
		CreatedByService:     "billing-etl",
		WorkflowStage:        "seal",
		Tags: map[string]string{
			"workflow": "billing",
		},
	}, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("write document: %v", err)
	}
	if result.DesiredReplicaCount != 1 || result.AchievedReplicaCount != 1 {
		t.Fatalf("replica counts = %d/%d, want local non-production 1/1", result.DesiredReplicaCount, result.AchievedReplicaCount)
	}
	if result.Metadata.Length != expectedLength {
		t.Fatalf("write length = %d, want %d", result.Metadata.Length, expectedLength)
	}
	if !bytes.Equal(result.Metadata.LogicalSHA256, sum[:]) {
		t.Fatal("write logical sha was not returned")
	}
	prepared, err := app.prepare.Recover()
	if err != nil {
		t.Fatalf("recover prepare log: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Identity != doc {
		t.Fatalf("prepare log records = %#v, want written document", prepared)
	}

	head, err := app.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head document: %v", err)
	}
	if head.Identity != doc || head.ContentType != "application/xml" || !head.HasContentType {
		t.Fatalf("head metadata = %#v, want written document", head)
	}

	readLength := uint64(4)
	sender := &recordingReadSender{}
	err = app.ReadDocument(context.Background(), api.ReadDocumentRequest{
		Identity: doc,
		Range: &api.ReadRange{
			Offset: 3,
			Length: &readLength,
		},
	}, sender)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want local", sender.metadata.Source)
	}
	if sender.metadata.SelectedRange.Offset != 3 || sender.metadata.SelectedRange.Length == nil || *sender.metadata.SelectedRange.Length != 4 {
		t.Fatalf("selected range = %#v, want offset 3 length 4", sender.metadata.SelectedRange)
	}
	if got := string(bytes.Join(sender.chunks, nil)); got != "3456" {
		t.Fatalf("read bytes = %q, want 3456", got)
	}

	found, err := app.FindDocuments(context.Background(), api.FindDocumentsRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Filter: api.DocumentFilter{
			DocumentNamePrefix:    "invoice",
			HasDocumentNamePrefix: true,
			DocumentClass:         scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			HasDocumentClass:      true,
			Tags: map[string]string{
				"workflow": "billing",
			},
		},
	})
	if err != nil {
		t.Fatalf("find documents: %v", err)
	}
	if len(found.Documents) != 1 || found.Documents[0].Identity != doc {
		t.Fatalf("found documents = %#v, want written document", found.Documents)
	}

	transaction, err := app.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if transaction.DocumentCount != 1 || transaction.PermanentDocumentCount != 1 {
		t.Fatalf("transaction counts = %#v, want one permanent document", transaction)
	}

	completedAt := time.Unix(200, 0).UTC()
	app.now = fixedClock(completedAt)
	transaction, err = app.CompleteTransaction(context.Background(), api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags: map[string]string{
			"closed_by": "test",
		},
	})
	if err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	if transaction.State != scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED {
		t.Fatalf("transaction state = %s, want completed", transaction.State)
	}
	if transaction.CompletedAt == nil || !transaction.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	}
}

func TestExpectedChecksumMismatchLeavesDocumentInvisible(t *testing.T) {
	dir := t.TempDir()
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	doc := testDocumentIdentity()
	badSHA := bytes.Repeat([]byte{9}, 32)

	_, err = app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ExpectedSHA256:   badSHA,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("real bytes")}))
	requireCode(t, err, codes.InvalidArgument)

	_, err = app.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	_, err = reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	prepared, err := reopened.prepare.Recover()
	if err != nil {
		t.Fatalf("recover prepare log: %v", err)
	}
	if len(prepared) != 0 {
		t.Fatalf("prepare records = %#v, want no visible prepare for rejected write", prepared)
	}
}

func TestCommittedWriteSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("durable local bytes")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head after reopen: %v", err)
	}
	if head.Length != uint64(len(data)) {
		t.Fatalf("head length after reopen = %d, want %d", head.Length, len(data))
	}
	sender := &recordingReadSender{}
	if err := reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read after reopen: %v", err)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read after reopen = %q, want %q", got, data)
	}
}

func TestMetadataProjectionRebuildsFromAuthorityLog(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("durable local bytes")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	app.now = fixedClock(time.Unix(100, 0).UTC())
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	completedAt := time.Unix(200, 0).UTC()
	app.now = fixedClock(completedAt)
	if _, err := app.CompleteTransaction(context.Background(), api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags: map[string]string{
			"closed_by": "projection-rebuild-test",
		},
	}); err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(dir, "metadata")); err != nil {
		t.Fatalf("remove metadata projection: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head rebuilt document: %v", err)
	}
	if head.Length != uint64(len(data)) {
		t.Fatalf("rebuilt length = %d, want %d", head.Length, len(data))
	}
	transaction, err := reopened.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	if err != nil {
		t.Fatalf("get rebuilt transaction: %v", err)
	}
	if transaction.CompletedAt == nil || !transaction.CompletedAt.Equal(completedAt) {
		t.Fatalf("rebuilt completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	}
	if transaction.Tags["closed_by"] != "projection-rebuild-test" {
		t.Fatalf("rebuilt transaction tags = %#v", transaction.Tags)
	}
	sender := &recordingReadSender{}
	if err := reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read rebuilt document: %v", err)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read rebuilt document = %q, want %q", got, data)
	}
}

func TestIdempotentReplayReturnsExistingDocumentWithoutAppending(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("invoice")
	length := uint64(len(data))
	sum := sha256.Sum256(data)
	init := api.WriteDocumentInit{
		Identity:             doc,
		DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ExpectedLength:       &length,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: "replay-key",
		CreatedByService:     "billing-etl",
	}

	first, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("replay write: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatal("second write was not marked as idempotent replay")
	}
	if !bytes.Equal(second.Metadata.LogicalSHA256, first.Metadata.LogicalSHA256) {
		t.Fatal("replay metadata did not match original write")
	}

	transaction, err := app.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if transaction.DocumentCount != 1 {
		t.Fatalf("document count = %d, want replay to count once", transaction.DocumentCount)
	}
}

func TestDuplicateWriteWithoutMatchingIdempotencyKeyIsConflict(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	init := api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}
	if _, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{[]byte("first")})); err != nil {
		t.Fatalf("first write: %v", err)
	}

	_, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{[]byte("second")}))
	requireCode(t, err, codes.AlreadyExists)
}

func openTestApplication(t *testing.T) *Application {
	t.Helper()
	app, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Fatalf("close app: %v", err)
		}
	})
	return app
}

func testDocumentIdentity() identity.Document {
	return identity.Document{
		TenantID:      "tenant",
		TransactionID: "txn",
		DocumentName:  "invoice.xml",
	}
}

type chunkReaderStub struct {
	chunks [][]byte
	index  int
}

func newChunkReader(chunks [][]byte) *chunkReaderStub {
	return &chunkReaderStub{chunks: chunks}
}

func (r *chunkReaderStub) Recv() ([]byte, error) {
	if r.index >= len(r.chunks) {
		return nil, io.EOF
	}
	chunk := r.chunks[r.index]
	r.index++
	return chunk, nil
}

type recordingReadSender struct {
	metadata api.ReadDocumentMetadata
	chunks   [][]byte
}

func (s *recordingReadSender) SendMetadata(metadata api.ReadDocumentMetadata) error {
	s.metadata = metadata
	return nil
}

func (s *recordingReadSender) SendChunk(data []byte) error {
	s.chunks = append(s.chunks, append([]byte(nil), data...))
	return nil
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %s", code)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("error = EOF, want status code %s", code)
	}
	if got := status.Code(err); got != code {
		t.Fatalf("code = %s, want %s; err = %v", got, code, err)
	}
}
