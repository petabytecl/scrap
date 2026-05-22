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
	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	storedDocument, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	if intent.State != metastore.UploadStatePending ||
		intent.BackendObjectKey != "blocks/"+storedDocument.Location.BlockID+".blk" {
		t.Fatalf("upload intent = %#v, want pending block upload", intent)
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
	if got := app.blocks.CurrentBlockLength(); got != blockstore.HeaderLength {
		t.Fatalf("open block length = %d, want rejected write rollback to header length %d", got, blockstore.HeaderLength)
	}

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
	storedDocument, err := reopened.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head rebuilt document for upload intent: %v", err)
	}
	intent, err := reopened.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	if err != nil {
		t.Fatalf("get rebuilt upload intent: %v", err)
	}
	if intent.State != metastore.UploadStatePending {
		t.Fatalf("rebuilt upload intent = %#v, want pending", intent)
	}
	sender := &recordingReadSender{}
	if err := reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read rebuilt document: %v", err)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read rebuilt document = %q, want %q", got, data)
	}
}

func TestBackendUploadProcessorUploadsPendingIntentAndReplaysOutcome(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("backend upload bytes")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	storedDocument, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if _, err := app.blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal current block: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	result, err := app.BackendUploadProcessor(backendStore).RunOnce(ctx)
	if err != nil {
		t.Fatalf("run backend upload processor: %v", err)
	}
	if result.Scanned != 1 || result.Uploaded != 1 || result.Failed != 0 {
		t.Fatalf("upload result = %#v, want one uploaded intent", result)
	}
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	if intent.State != metastore.UploadStateUploaded {
		t.Fatalf("upload intent = %#v, want uploaded", intent)
	}
	if _, err := backendStore.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		t.Fatalf("head backend object: %v", err)
	}
	if _, err := backendStore.HeadObject(ctx, intent.IndexObjectKey); err != nil {
		t.Fatalf("head backend index object: %v", err)
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
	intent, err = reopened.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	if err != nil {
		t.Fatalf("get rebuilt upload intent: %v", err)
	}
	if intent.State != metastore.UploadStateUploaded {
		t.Fatalf("rebuilt upload intent = %#v, want uploaded", intent)
	}
}

func TestRunBackendUploadOnceSealsDueBlockAndUploads(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("seal and upload")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	storedDocument, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}

	result, err := app.RunBackendUploadOnce(ctx, backendStore)
	if err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	if !result.Sealed || result.SealedBlockID != storedDocument.Location.BlockID {
		t.Fatalf("seal result = %#v, want sealed block %q", result, storedDocument.Location.BlockID)
	}
	if result.Upload.Scanned != 1 || result.Upload.Uploaded != 1 || result.Upload.Failed != 0 || result.Upload.Deferred != 0 {
		t.Fatalf("upload result = %#v, want one uploaded sealed block", result.Upload)
	}
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	if intent.State != metastore.UploadStateUploaded {
		t.Fatalf("upload intent = %#v, want uploaded", intent)
	}
	if _, err := backendStore.HeadObject(ctx, intent.IndexObjectKey); err != nil {
		t.Fatalf("head backend index object: %v", err)
	}
}

func TestReadDocumentFallsBackToVerifiedBackendCopy(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(250, 0).UTC())
	data := []byte("backend fallback bytes")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(stored.Location.StoredOffset)); err != nil {
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}

	sender := &recordingReadSender{}
	if err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_BACKEND {
		t.Fatalf("read source = %s, want backend", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read bytes = %q, want %q", got, data)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 1 ||
		queue[0].GetTarget().GetDocument().GetDocumentName() != doc.DocumentName ||
		queue[0].GetReason() == "" ||
		queue[0].GetDetectedAt().AsTime() != time.Unix(250, 0).UTC() {
		t.Fatalf("repair queue = %#v, want quarantined local ref", queue)
	}
}

func TestRunQueuedOperationsOnceRestoresDocumentFromBackend(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("restore me from backend")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "restore-cold-1", time.Unix(200, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
	}
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}
	operation := queuedOperation("restore-op-1", "restore", []*adminv1.Target{
		{
			Target: &adminv1.Target_Document{
				Document: &adminv1.DocumentTarget{
					TenantId:      doc.TenantID,
					TransactionId: doc.TransactionID,
					DocumentName:  doc.DocumentName,
				},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one restore success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetWorkUnitsCompleted() != 1 {
		t.Fatalf("finished operation = %#v, want succeeded restore", finished)
	}
	restored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head restored document: %v", err)
	}
	if restored.RestoreState != metastore.RestoreStateHot || restored.Availability != metastore.AvailabilityHot {
		t.Fatalf("restore state = %d/%d, want hot", restored.RestoreState, restored.Availability)
	}
	sender := &recordingReadSender{}
	if err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read restored document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want restored local", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("restored bytes = %q, want %q", got, data)
	}
}

func TestRunQueuedOperationsOnceRepairsQuarantinedLocalBlock(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("repair me from backend")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(stored.Location.StoredOffset)); err != nil {
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}
	if err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{}); err != nil {
		t.Fatalf("read with backend fallback: %v", err)
	}
	operation := queuedOperation("repair-op-1", "repair", []*adminv1.Target{
		{
			Target: &adminv1.Target_Document{
				Document: &adminv1.DocumentTarget{
					TenantId:      doc.TenantID,
					TransactionId: doc.TransactionID,
					DocumentName:  doc.DocumentName,
				},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one repair success", result)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("repair queue = %#v, want resolved repair", queue)
	}
	sender := &recordingReadSender{}
	if err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read repaired document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want repaired local", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("repaired bytes = %q, want %q", got, data)
	}
}

func TestReadDocumentReturnsRestorePendingDetail(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("cold bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateRestorePending, "restore requested", "restore-state-1", time.Unix(200, 0).UTC()); err != nil {
		t.Fatalf("update restore state: %v", err)
	}

	sender := &recordingReadSender{}
	err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	if detail.GetIdentity().GetDocumentName() != doc.DocumentName ||
		len(detail.GetAffectedBlockIds()) != 1 ||
		detail.GetRestoreState() != "restore_pending" ||
		!detail.GetRestoreQueued() ||
		!detail.GetRetryHint().GetRetryable() {
		t.Fatalf("restore detail = %#v, want restore-pending detail", detail)
	}
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before restore-pending error", sender.sentMetadata, len(sender.chunks))
	}
}

func TestReadDocumentReturnsCryptoUnavailableDetail(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("encrypted bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCryptoUnavailable, "key unavailable", "crypto-state-1", time.Unix(201, 0).UTC()); err != nil {
		t.Fatalf("update restore state: %v", err)
	}

	sender := &recordingReadSender{}
	err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	detail := requireCryptoUnavailableDetail(t, err)
	if detail.GetIdentity().GetDocumentName() != doc.DocumentName ||
		detail.GetKeyScope() != "backend" ||
		!detail.GetRetryHint().GetRetryable() {
		t.Fatalf("crypto detail = %#v, want crypto-unavailable detail", detail)
	}
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before crypto-unavailable error", sender.sentMetadata, len(sender.chunks))
	}
}

func TestCorruptReadFailsBeforeSendingMetadata(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("verified before metadata")
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(stored.Location.StoredOffset)); err != nil {
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}

	sender := &recordingReadSender{}
	err = app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.DataLoss)
	detail := requireIntegrityDetail(t, err)
	if detail.GetIdentity().GetDocumentName() != doc.DocumentName ||
		len(detail.GetAttemptedSources()) != 1 ||
		detail.GetAttemptedSources()[0] != "local" ||
		detail.GetEvidenceId() == "" {
		t.Fatalf("integrity detail = %#v, want local source and evidence", detail)
	}
	if sender.sentMetadata {
		t.Fatal("metadata was sent before corruption was detected")
	}
	if len(sender.chunks) != 0 {
		t.Fatalf("sent %d chunks before corruption error", len(sender.chunks))
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

func TestRunQueuedOperationsOnceAppliesDocumentTombstone(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("delete me")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	operation := queuedTombstoneOperation("tombstone-op-1", []*adminv1.Target{
		{
			Target: &adminv1.Target_Document{
				Document: &adminv1.DocumentTarget{
					TenantId:      doc.TenantID,
					TransactionId: doc.TransactionID,
					DocumentName:  doc.DocumentName,
				},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetFinishedAt() == nil ||
		finished.GetProgress().GetWorkUnitsCompleted() != 1 {
		t.Fatalf("finished operation = %#v, want succeeded", finished)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head document: %v", err)
	}
	if stored.LifecycleState != metastore.LifecycleStateTombstoned ||
		stored.TombstoneOperationID != operation.GetOperationId() {
		t.Fatalf("stored document = %#v, want tombstoned by operation", stored)
	}
}

func TestGetAdminDocumentReturnsPhysicalReference(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(200, 0).UTC())
	doc := testDocumentIdentity()
	data := []byte("inspect me")
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head document: %v", err)
	}

	adminDoc, err := app.GetAdminDocument(ctx, doc)
	if err != nil {
		t.Fatalf("get admin document: %v", err)
	}
	if adminDoc.GetShardId() != "local" ||
		adminDoc.GetLength() != uint64(len(data)) ||
		!bytes.Equal(adminDoc.GetLogicalSha256(), stored.LogicalSHA256[:]) ||
		adminDoc.GetDocument().GetTenantId() != doc.TenantID ||
		adminDoc.GetDocument().GetTransactionId() != doc.TransactionID ||
		adminDoc.GetDocument().GetDocumentName() != doc.DocumentName ||
		len(adminDoc.GetBlockIds()) != 1 ||
		adminDoc.GetBlockIds()[0] != stored.Location.BlockID ||
		adminDoc.GetRepairRequired() {
		t.Fatalf("admin document = %#v, want stored physical metadata", adminDoc)
	}

	adminBlock, err := app.GetAdminBlock(ctx, api.BlockTarget{ShardID: "local", BlockID: stored.Location.BlockID})
	if err != nil {
		t.Fatalf("get admin block: %v", err)
	}
	if adminBlock.GetShardId() != "local" ||
		adminBlock.GetBlockId() != stored.Location.BlockID ||
		adminBlock.GetLength() != blockstore.HeaderLength+uint64(len(data)) ||
		len(adminBlock.GetChecksum()) != sha256.Size ||
		len(adminBlock.GetReplicaMemberIds()) != 1 ||
		adminBlock.GetReplicaMemberIds()[0] != "local" ||
		adminBlock.GetBackendObjectKey() != "blocks/"+stored.Location.BlockID+".blk" {
		t.Fatalf("admin block = %#v, want local block metadata", adminBlock)
	}

	shard, err := app.GetAdminShard(ctx, "local")
	if err != nil {
		t.Fatalf("get admin shard: %v", err)
	}
	if shard.GetShardId() != "local" ||
		shard.GetLeaderMemberId() != "local" ||
		len(shard.GetVoterMemberIds()) != 1 ||
		shard.GetVoterMemberIds()[0] != "local" ||
		shard.GetCommittedIndex() == 0 ||
		shard.GetAppliedIndex() != shard.GetCommittedIndex() {
		t.Fatalf("admin shard = %#v, want local shard metadata", shard)
	}

	member, err := app.GetAdminMember(ctx, "local")
	if err != nil {
		t.Fatalf("get admin member: %v", err)
	}
	if member.GetStorageMemberId() != "local" ||
		member.GetCellId() != "local" ||
		member.GetState() != adminv1.MemberState_MEMBER_STATE_ONLINE ||
		member.GetBytesUsed() == 0 ||
		member.GetBytesCapacity() == 0 ||
		member.GetLastSeenAt().AsTime() != time.Unix(200, 0).UTC() {
		t.Fatalf("admin member = %#v, want local member metadata", member)
	}

	summary, err := app.GetAdminClusterSummary(ctx)
	if err != nil {
		t.Fatalf("get admin cluster summary: %v", err)
	}
	if summary.GetShardCount() != 1 ||
		summary.GetStorageMemberCount() != 1 ||
		summary.GetLocalBytesUsed() == 0 ||
		summary.GetLocalBytesCapacity() == 0 {
		t.Fatalf("cluster summary = %#v, want local single-member summary", summary)
	}

	runway, err := app.GetAdminCapacityRunway(ctx, "")
	if err != nil {
		t.Fatalf("get admin capacity runway: %v", err)
	}
	if runway.GetCapacityProfileId() != "local-non-production" ||
		runway.GetUsableBytesRemaining() == 0 ||
		runway.GetEstimatedBytesPerDay() != 0 ||
		runway.GetRunwayDays() != 0 ||
		len(runway.GetWarnings()) == 0 {
		t.Fatalf("capacity runway = %#v, want non-production capacity warning", runway)
	}
}

func TestLocalMemberCordonStatePersists(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	member, err := app.CordonMember(ctx, api.MemberMutationRequest{
		OperationID:   "cordon-1",
		StorageMember: "local",
		Reason:        "maintenance",
	})
	if err != nil {
		t.Fatalf("cordon member: %v", err)
	}
	if !member.GetCordoned() {
		t.Fatalf("member = %#v, want cordoned", member)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	member, err = reopened.GetAdminMember(ctx, "local")
	if err != nil {
		t.Fatalf("get reopened member: %v", err)
	}
	if !member.GetCordoned() {
		t.Fatalf("reopened member = %#v, want persisted cordon", member)
	}
	safety, err := reopened.GetEvictionSafety(ctx, "local")
	if err != nil {
		t.Fatalf("get eviction safety: %v", err)
	}
	if safety.GetSafeToEvict() || len(safety.GetWarnings()) == 0 {
		t.Fatalf("eviction safety = %#v, want unsafe single-member warning", safety)
	}
	member, err = reopened.UncordonMember(ctx, api.MemberMutationRequest{
		OperationID:   "uncordon-1",
		StorageMember: "local",
	})
	if err != nil {
		t.Fatalf("uncordon member: %v", err)
	}
	if member.GetCordoned() {
		t.Fatalf("member = %#v, want uncordoned", member)
	}
}

func TestCordonedLocalMemberRejectsNewWritesButAllowsReplay(t *testing.T) {
	ctx := context.Background()
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
		ClientIdempotencyKey: "write-1",
		CreatedByService:     "billing-etl",
	}
	if _, err := app.WriteDocument(ctx, init, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write initial document: %v", err)
	}
	if _, err := app.CordonMember(ctx, api.MemberMutationRequest{
		OperationID:   "cordon-1",
		StorageMember: "local",
		Reason:        "maintenance",
	}); err != nil {
		t.Fatalf("cordon member: %v", err)
	}

	newDoc := init
	newDoc.Identity.DocumentName = "new-invoice.xml"
	_, err := app.WriteDocument(ctx, newDoc, newChunkReader([][]byte{[]byte("new invoice")}))
	requireCode(t, err, codes.FailedPrecondition)

	replayed, err := app.WriteDocument(ctx, init, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("replay existing document while cordoned: %v", err)
	}
	if !replayed.IdempotentReplay {
		t.Fatalf("replay = %#v, want idempotent replay", replayed)
	}
}

func TestLocalRecoveryReadinessFailsClosedWithoutPublishedMetadata(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	readiness, err := app.GetRecoveryReadiness(ctx)
	if err != nil {
		t.Fatalf("get recovery readiness: %v", err)
	}
	if readiness.GetReady() || len(readiness.GetWarnings()) < 2 {
		t.Fatalf("readiness = %#v, want not ready with missing metadata/backend warnings", readiness)
	}
}

func TestPublishMetadataSnapshotWritesCurrentPointerAndUpdatesReadiness(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(500, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("published metadata bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("published metadata bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}

	publication, err := app.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}
	if publication.DocumentCount != 1 || publication.PointerKey == "" || publication.ManifestKey == "" || publication.SnapshotKey == "" {
		t.Fatalf("publication = %#v, want one-document publication with object keys", publication)
	}
	pointer, err := published.UnmarshalCurrentPointer(readBackendObject(t, ctx, backendStore, publication.PointerKey))
	if err != nil {
		t.Fatalf("unmarshal current pointer: %v", err)
	}
	if pointer.GetManifestId() != publication.Manifest.GetManifestId() ||
		pointer.GetPublishedAt().AsTime() != app.now() {
		t.Fatalf("pointer = %#v, want published manifest and timestamp", pointer)
	}

	readiness, err := app.GetRecoveryReadiness(ctx)
	if err != nil {
		t.Fatalf("get recovery readiness: %v", err)
	}
	if !readiness.GetReady() || readiness.GetLatestRestorableCheckpointAt().AsTime() != app.now() ||
		!hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_NON_PRODUCTION_MODE") ||
		hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_METADATA_EXPORT_MISSING") {
		t.Fatalf("readiness = %#v, want non-production-ready checkpoint timestamp", readiness)
	}
}

func TestRunQueuedOperationsOnceCopyVerifySucceedsWithPublishedCheckpoint(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(600, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("copy verify bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("copy verify bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := app.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}

	store := openTestOperationStore(t)
	operation := queuedOperation("copy-verify-op-1", "copy-verify", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one successful copy verification", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["manifest_id"] != publication.Manifest.GetManifestId() ||
		finished.GetProgress().GetCounters()["verified_objects"] == "" {
		t.Fatalf("finished operation = %#v, want successful verified checkpoint", finished)
	}
}

func TestRunQueuedOperationsOnceMetadataRestoreImportsColdDocuments(t *testing.T) {
	ctx := context.Background()
	source := openTestApplication(t)
	source.now = fixedClock(time.Unix(700, 0).UTC())
	source.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("metadata restore bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	source.SetBackendStore(backendStore)
	doc := testDocumentIdentity()
	if _, err := source.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("metadata restore bytes")})); err != nil {
		t.Fatalf("write source document: %v", err)
	}
	if _, err := source.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := source.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}

	restoredApp := openTestApplication(t)
	restoredApp.now = fixedClock(time.Unix(701, 0).UTC())
	restoredApp.SetBackendStore(backendStore)
	store := openTestOperationStore(t)
	operation := queuedOperation("metadata-restore-op-1", "metadata-restore", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := restoredApp.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one successful metadata restore", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["documents"] != "1" ||
		finished.GetProgress().GetCounters()["upload_intents"] != "1" {
		t.Fatalf("finished operation = %#v, want imported document and upload intent", finished)
	}

	restored, err := restoredApp.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head restored document: %v", err)
	}
	if restored.Availability != metastore.AvailabilityCold ||
		restored.RestoreState != metastore.RestoreStateCold ||
		restored.UploadState != metastore.UploadStateUploaded {
		t.Fatalf("restored document state = %#v, want cold uploaded metadata", restored)
	}
	intent, err := restoredApp.metadata.GetUploadIntent(restored.Location.BlockID)
	if err != nil {
		t.Fatalf("get restored upload intent: %v", err)
	}
	if intent.State != metastore.UploadStateUploaded || intent.BackendObjectKey == "" {
		t.Fatalf("restored intent = %#v, want uploaded backend object", intent)
	}
	err = restoredApp.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	if detail.GetRestoreState() != "cold" {
		t.Fatalf("restore detail = %#v, want cold restore state", detail)
	}
}

func TestRunQueuedOperationsOnceFailsNotReadyDROperation(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-op-1", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: "snapshot-1"},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR operation", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DR_DRILL_FAILED" {
		t.Fatalf("finished operation = %#v, want DR drill failure", finished)
	}
}

func TestRunQueuedOperationsOnceDryRunDROperationReportsReadiness(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(800, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("dry-run drill bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("dry-run drill bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := app.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}
	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-dry-run-1", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	operation.DryRun = true
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one dry-run success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["documents"] != "1" ||
		finished.GetProgress().GetCounters()["upload_intents"] != "1" {
		t.Fatalf("finished operation = %#v, want dry-run checkpoint verification", finished)
	}
}

func TestRunQueuedOperationsOnceDRDrillRestoresScratchMetadata(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(801, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("scratch drill bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("scratch drill bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := app.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}
	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-op-2", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one successful DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["documents"] != "1" ||
		finished.GetProgress().GetCounters()["upload_intents"] != "1" {
		t.Fatalf("finished operation = %#v, want scratch drill restore counters", finished)
	}
}

func TestRunQueuedOperationsOnceFailsUnsafeDrainInSingleMemberMode(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	operation := queuedOperation("drain-op-1", "drain", []*adminv1.Target{
		{
			Target: &adminv1.Target_StorageMember{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed drain", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DRAIN_UNSAFE" ||
		len(finished.GetWarnings()) == 0 {
		t.Fatalf("finished operation = %#v, want unsafe drain failure with warnings", finished)
	}
	member, err := app.GetAdminMember(ctx, "local")
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.GetState() == adminv1.MemberState_MEMBER_STATE_DRAINING || member.GetCordoned() {
		t.Fatalf("member = %#v, want failed drain to leave local member online and uncordoned", member)
	}
}

func TestRunQueuedOperationsOnceDryRunDrainDoesNotMutateMember(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	operation := queuedOperation("drain-dry-run-1", "drain", []*adminv1.Target{
		{
			Target: &adminv1.Target_StorageMember{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: "local"},
			},
		},
	})
	operation.DryRun = true
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want dry-run success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetWorkUnitsCompleted() != 1 ||
		len(finished.GetWarnings()) == 0 {
		t.Fatalf("finished operation = %#v, want dry-run success with safety warning", finished)
	}
	member, err := app.GetAdminMember(ctx, "local")
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.GetState() == adminv1.MemberState_MEMBER_STATE_DRAINING || member.GetCordoned() {
		t.Fatalf("member = %#v, want dry-run drain to leave local member online and uncordoned", member)
	}
}

func TestRunQueuedOperationsOnceAppliesTransactionTombstone(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	first := testDocumentIdentity()
	second := identity.Document{TenantID: first.TenantID, TransactionID: first.TransactionID, DocumentName: "summary.pdf"}
	for _, doc := range []identity.Document{first, second} {
		if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
			Identity:         doc,
			DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
			PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
			CreatedByService: "billing-etl",
		}, newChunkReader([][]byte{[]byte(doc.DocumentName)})); err != nil {
			t.Fatalf("write document %s: %v", doc.DocumentName, err)
		}
	}
	operation := queuedTombstoneOperation("tombstone-op-2", []*adminv1.Target{
		{
			Target: &adminv1.Target_Transaction{
				Transaction: &adminv1.TransactionTarget{
					TenantId:      first.TenantID,
					TransactionId: first.TransactionID,
				},
			},
		},
	})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one success", result)
	}
	for _, doc := range []identity.Document{first, second} {
		stored, err := app.metadata.HeadDocument(doc)
		if err != nil {
			t.Fatalf("head document %s: %v", doc.DocumentName, err)
		}
		if stored.LifecycleState != metastore.LifecycleStateTombstoned ||
			stored.TombstoneOperationID != operation.GetOperationId() {
			t.Fatalf("stored document %s = %#v, want tombstoned by operation", doc.DocumentName, stored)
		}
	}
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

func openTestOperationStore(t *testing.T) *operations.Store {
	t.Helper()
	store, err := operations.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open operation store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close operation store: %v", err)
		}
	})
	return store
}

func queuedTombstoneOperation(operationID string, targets []*adminv1.Target) *adminv1.Operation {
	return queuedOperation(operationID, "tombstone", targets)
}

func queuedOperation(operationID string, operationType string, targets []*adminv1.Target) *adminv1.Operation {
	return &adminv1.Operation{
		OperationId:         operationID,
		OperationType:       operationType,
		State:               adminv1.OperationState_OPERATION_STATE_QUEUED,
		RequestedByIdentity: "test",
		RequestedAt:         timestamppb.New(time.Unix(300, 0).UTC()),
		Targets:             targets,
		Progress:            &adminv1.OperationProgress{Message: "queued"},
	}
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
	metadata     api.ReadDocumentMetadata
	sentMetadata bool
	chunks       [][]byte
}

func (s *recordingReadSender) SendMetadata(metadata api.ReadDocumentMetadata) error {
	s.metadata = metadata
	s.sentMetadata = true
	return nil
}

func (s *recordingReadSender) SendChunk(data []byte) error {
	s.chunks = append(s.chunks, append([]byte(nil), data...))
	return nil
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func ptr[T any](value T) *T {
	return &value
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

func readBackendObject(t *testing.T, ctx context.Context, store backend.Store, key string) []byte {
	t.Helper()
	var got bytes.Buffer
	if err := store.ReadObjectRange(ctx, key, backend.Range{}, &got); err != nil {
		t.Fatalf("read backend object %q: %v", key, err)
	}
	return got.Bytes()
}

func hasWarningCode(warnings []*adminv1.OperationWarning, code string) bool {
	for _, warning := range warnings {
		if warning.GetCode() == code {
			return true
		}
	}
	return false
}

func requireIntegrityDetail(t *testing.T, err error) *scrapv1.IntegrityFailureDetail {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	for _, detail := range st.Details() {
		if integrity, ok := detail.(*scrapv1.IntegrityFailureDetail); ok {
			return integrity
		}
	}
	t.Fatalf("status details = %#v, want IntegrityFailureDetail", st.Details())
	return nil
}

func requireRestorePendingDetail(t *testing.T, err error) *scrapv1.RestorePendingDetail {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	for _, detail := range st.Details() {
		if restore, ok := detail.(*scrapv1.RestorePendingDetail); ok {
			return restore
		}
	}
	t.Fatalf("status details = %#v, want RestorePendingDetail", st.Details())
	return nil
}

func requireCryptoUnavailableDetail(t *testing.T, err error) *scrapv1.CryptoUnavailableDetail {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	for _, detail := range st.Details() {
		if crypto, ok := detail.(*scrapv1.CryptoUnavailableDetail); ok {
			return crypto
		}
	}
	t.Fatalf("status details = %#v, want CryptoUnavailableDetail", st.Details())
	return nil
}
