package localstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/storageformat"
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
		intent.BackendObjectKey != "blocks/"+storedDocument.Location.BlockID+".blk" ||
		intent.IndexObjectKey != "blocks/"+storedDocument.Location.BlockID+".idx" ||
		intent.EnvelopeObjectKey != "blocks/"+storedDocument.Location.BlockID+".env" {
		t.Fatalf("upload intent = %#v, want pending block/index/envelope upload", intent)
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

func TestWriteDocumentPreparesPeersBeforeMetadataVisibility(t *testing.T) {
	app := openTestApplication(t)
	data := []byte("peer prepared bytes")
	doc := testDocumentIdentity()
	doc.DocumentName = "peer-prepare.xml"
	peer1 := newRecordingPreparePeer("member-1")
	peer2 := newRecordingPreparePeer("member-2")
	peer1.beforeReceipt = func(request replication.PrepareRequest) {
		if _, err := app.metadata.HeadDocument(request.Document.Identity); !errors.Is(err, metastore.ErrNotFound) {
			t.Fatalf("document visible during peer prepare: %v", err)
		}
	}
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 3, QuorumReplicaCount: 3}
	app.peerPrepareTargets = []replication.Target{
		{MemberID: "member-1", Preparer: peer1},
		{MemberID: "member-2", Preparer: peer2},
	}

	result, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("write document: %v", err)
	}
	if result.DesiredReplicaCount != 3 || result.AchievedReplicaCount != 3 {
		t.Fatalf("replica counts = %d/%d, want 3/3", result.DesiredReplicaCount, result.AchievedReplicaCount)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if len(stored.Location.Replicas) != 2 {
		t.Fatalf("replicas = %#v, want two peer prepare receipts", stored.Location.Replicas)
	}
	if peer1.prepareCount != 1 || peer2.prepareCount != 1 {
		t.Fatalf("peer prepare counts = %d/%d, want 1/1", peer1.prepareCount, peer2.prepareCount)
	}
	if !bytes.Equal(peer1.preparedBytes, data) || !bytes.Equal(peer2.preparedBytes, data) {
		t.Fatalf("prepared bytes = %q/%q, want %q", peer1.preparedBytes, peer2.preparedBytes, data)
	}
}

func TestWriteDocumentPeerPrepareFailureLeavesDocumentInvisible(t *testing.T) {
	app := openTestApplication(t)
	data := []byte("partial peer prepare")
	doc := testDocumentIdentity()
	doc.DocumentName = "peer-prepare-failure.xml"
	peer1 := newRecordingPreparePeer("member-1")
	peer2 := newRecordingPreparePeer("member-2")
	peer2.errAfterRead = errors.New("partial transfer")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 3, QuorumReplicaCount: 3}
	app.peerPrepareTargets = []replication.Target{
		{MemberID: "member-1", Preparer: peer1},
		{MemberID: "member-2", Preparer: peer2},
	}

	_, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	requireCode(t, err, codes.Unavailable)
	if _, err := app.metadata.HeadDocument(doc); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("head document after failed peer prepare = %v, want %v", err, metastore.ErrNotFound)
	}
	if peer1.prepareCount != 1 || peer2.prepareCount != 1 {
		t.Fatalf("peer prepare counts = %d/%d, want 1/1", peer1.prepareCount, peer2.prepareCount)
	}
}

func TestStrongMetadataReadsFailClosedWithoutReadIndex(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("fresh metadata")
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	replaceAuthorityFreshness(t, app, staticFreshnessChecker{readErr: raftmeta.ErrReadFreshnessUnavailable})

	_, err := app.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.Unavailable)

	sender := &recordingReadSender{}
	err = app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("read sent metadata=%v chunks=%d before freshness proof", sender.sentMetadata, len(sender.chunks))
	}

	_, err = app.FindDocuments(context.Background(), api.FindDocumentsRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	requireCode(t, err, codes.Unavailable)
	_, err = app.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	requireCode(t, err, codes.Unavailable)
}

func TestWriteDocumentFailsClosedWhenLeaderIsStale(t *testing.T) {
	app := openTestApplication(t)
	replaceAuthorityFreshness(t, app, staticFreshnessChecker{writeErr: raftmeta.ErrNotLeader})
	doc := testDocumentIdentity()

	_, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("stale leader must not ack")}))
	requireCode(t, err, codes.FailedPrecondition)
	if got := app.blocks.CurrentBlockLength(); got != blockstore.HeaderLength {
		t.Fatalf("open block length = %d, want stale leader rejection before byte append", got)
	}
	_, err = app.metadata.HeadDocument(doc)
	if !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("metadata error = %v, want %v", err, metastore.ErrNotFound)
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

func TestUnsafeCapacityAdmissionLeavesDocumentInvisible(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.minUsableBytesAfterWrite = ^uint64(0)
	doc := testDocumentIdentity()
	data := []byte("capacity rejected bytes")
	expectedLength := uint64(len(data))

	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ExpectedLength:   &expectedLength,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	requireCode(t, err, codes.ResourceExhausted)
	detail := requireUnsafeCapacityDetail(t, err)
	if detail.GetCapacityProfileId() != localCapacityProfileID ||
		detail.GetRequiredBytes() <= detail.GetAvailableBytes() ||
		len(detail.GetWarnings()) == 0 {
		t.Fatalf("capacity detail = %#v, want unsafe local capacity detail", detail)
	}
	if got := app.blocks.CurrentBlockLength(); got != blockstore.HeaderLength {
		t.Fatalf("open block length = %d, want rejected write to avoid append", got)
	}
	_, err = app.HeadDocument(ctx, api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
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

func TestCrashAfterBlockSyncLeavesDocumentInvisible(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("synced but unprepared bytes")
	init := writeInitForCrashBoundary(doc, data, "crash-after-block-sync")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	app.writeFaults.afterBlockSync = func(record blockstore.Record) error {
		if record.StoredLength != uint64(len(data)) {
			t.Fatalf("stored length = %d, want %d", record.StoredLength, len(data))
		}
		return errSimulatedLocalCrash
	}

	_, err = app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if !errors.Is(err, errSimulatedLocalCrash) {
		t.Fatalf("write error = %v, want simulated crash", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close crashed app: %v", err)
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
		t.Fatalf("prepared documents = %#v, want none after block-only crash", prepared)
	}
}

func TestCrashAfterPrepareSyncLeavesDocumentInvisible(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("prepared but uncommitted bytes")
	init := writeInitForCrashBoundary(doc, data, "crash-after-prepare-sync")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	app.writeFaults.afterPrepareSync = func(document metastore.Document) error {
		if document.Identity != doc {
			t.Fatalf("prepared document = %#v, want %v", document.Identity, doc)
		}
		return errSimulatedLocalCrash
	}

	_, err = app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if !errors.Is(err, errSimulatedLocalCrash) {
		t.Fatalf("write error = %v, want simulated crash", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close crashed app: %v", err)
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
	if len(prepared) != 1 || prepared[0].Identity != doc {
		t.Fatalf("prepared documents = %#v, want prepared document invisible", prepared)
	}
	readLength := prepared[0].Length
	if err := reopened.blocks.VerifyRange(prepared[0].Location, 0, &readLength); err != nil {
		t.Fatalf("verify prepared local bytes: %v", err)
	}
}

func TestCrashAfterMetadataApplyKeepsCommittedDocumentRetryable(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("metadata committed before ack")
	init := writeInitForCrashBoundary(doc, data, "crash-after-metadata-apply")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	app.writeFaults.afterMetadataApply = func(document metastore.Document) error {
		if document.Identity != doc {
			t.Fatalf("committed document = %#v, want %v", document.Identity, doc)
		}
		return errSimulatedLocalCrash
	}

	_, err = app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if !errors.Is(err, errSimulatedLocalCrash) {
		t.Fatalf("write error = %v, want simulated crash", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close crashed app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head committed document after reopen: %v", err)
	}
	if head.Length != uint64(len(data)) {
		t.Fatalf("head length = %d, want %d", head.Length, len(data))
	}
	stored, err := reopened.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head committed document from metadata: %v", err)
	}
	if _, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("upload intent before replay error = %v, want not found", err)
	}
	replayed, err := reopened.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("idempotent replay after metadata crash: %v", err)
	}
	if !replayed.IdempotentReplay {
		t.Fatal("retry after metadata crash was not an idempotent replay")
	}
	if _, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID); err != nil {
		t.Fatalf("upload intent after idempotent replay: %v", err)
	}
}

func TestCrashBeforeACKKeepsCommittedDocumentRetryable(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("fully committed before ack")
	init := writeInitForCrashBoundary(doc, data, "crash-before-ack")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	app.writeFaults.beforeACK = func(document metastore.Document) error {
		if document.Identity != doc {
			t.Fatalf("acked document = %#v, want %v", document.Identity, doc)
		}
		return errSimulatedLocalCrash
	}

	_, err = app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if !errors.Is(err, errSimulatedLocalCrash) {
		t.Fatalf("write error = %v, want simulated crash", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close crashed app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head committed document after reopen: %v", err)
	}
	stored, err := reopened.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head committed document from metadata: %v", err)
	}
	if _, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID); err != nil {
		t.Fatalf("get upload intent after ack-boundary crash: %v", err)
	}
	replayed, err := reopened.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	if err != nil {
		t.Fatalf("idempotent replay after ack-boundary crash: %v", err)
	}
	if !replayed.IdempotentReplay || replayed.Metadata.Identity != head.Identity {
		t.Fatalf("replay = %#v, want idempotent committed document", replayed)
	}
}

func TestPrepareLogRecoveryTruncatesCrashCutTail(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("valid prepared record")
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	if _, err := app.WriteDocument(context.Background(), writeInitForCrashBoundary(doc, data, "valid-prepare"), newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	appendPrepareLogTail(t, dir, []byte{0, 0, 0, 128, 'p', 'a', 'r'})
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app with crash-cut prepare log: %v", err)
	}
	defer reopened.Close()
	prepared, err := reopened.prepare.Recover()
	if err != nil {
		t.Fatalf("recover truncated prepare log: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Identity != doc {
		t.Fatalf("prepared documents = %#v, want valid committed document only", prepared)
	}
	payload, err := metastore.MarshalDocument(stored)
	if err != nil {
		t.Fatalf("marshal stored document: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, prepareLogName))
	if err != nil {
		t.Fatalf("stat prepare log: %v", err)
	}
	wantSize := int64(prepareLogHeaderLen + len(payload) + prepareLogCRCLen)
	if info.Size() != wantSize {
		t.Fatalf("prepare log size = %d, want truncated size %d", info.Size(), wantSize)
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
	queue, err := reopened.GetRepairQueue(context.Background(), "local")
	if err != nil {
		t.Fatalf("get repair queue after clean rebuild: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("repair queue after clean rebuild = %#v, want empty", queue)
	}
}

func TestMetadataProjectionRebuildQueuesRepairForMissingLocalRef(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("missing after projection rebuild")
	app, stored := writeDocumentForProjectionRebuild(t, dir, doc, data)
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
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
		t.Fatalf("head rebuilt missing-ref document: %v", err)
	}
	if head.Length != uint64(len(data)) {
		t.Fatalf("head length = %d, want %d", head.Length, len(data))
	}
	assertUnreadableRepairRef(t, reopened, doc, stored.Location.BlockID)
}

func TestMetadataProjectionRebuildQueuesRepairForCorruptLocalRef(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("corrupt after projection rebuild")
	app, stored := writeDocumentForProjectionRebuild(t, dir, doc, data)
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(stored.Location.StoredOffset)); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
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
	assertUnreadableRepairRef(t, reopened, doc, stored.Location.BlockID)
}

func TestOpenVerifiesLocalRefsWhenProjectionExists(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("corrupt without projection rebuild")
	app, stored := writeDocumentForProjectionRebuild(t, dir, doc, data)
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), int64(stored.Location.StoredOffset)); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	assertUnreadableRepairRef(t, reopened, doc, stored.Location.BlockID)
}

func TestByteServingReadinessVerificationIsCancelable(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("cancel byte readiness")
	app, _ := writeDocumentForProjectionRebuild(t, dir, doc, data)
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reopened, err := OpenWithContext(ctx, dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()

	err = reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read error = %v, want canceled readiness verification", err)
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
	if _, err := backendStore.HeadObject(ctx, intent.EnvelopeObjectKey); err != nil {
		t.Fatalf("head backend envelope object: %v", err)
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
	if !result.MetadataPublished || result.MetadataPublication == nil {
		t.Fatalf("metadata publication = %#v, want upload-triggered checkpoint", result.MetadataPublication)
	}
	if _, err := published.UnmarshalCurrentPointer(readBackendObject(t, ctx, backendStore, result.MetadataPublication.PointerKey)); err != nil {
		t.Fatalf("read published current pointer: %v", err)
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
	if _, err := backendStore.HeadObject(ctx, intent.EnvelopeObjectKey); err != nil {
		t.Fatalf("head backend envelope object: %v", err)
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

func TestReadDocumentWithTransitEnvelopeRequiresKeyMaterial(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(251, 0).UTC())
	data := []byte("backend encrypted fallback bytes")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 2})
	app.SetEnvelopeTransit(transit, "transit/backend")
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
	corruptStoredByte(t, app, stored.Location, 0)
	transit.SetUnavailable(true)

	sender := &recordingReadSender{}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	detail := requireCryptoUnavailableDetail(t, err)
	if detail.GetIdentity().GetDocumentName() != doc.DocumentName ||
		detail.GetKeyScope() != "backend" ||
		!detail.GetRetryHint().GetRetryable() {
		t.Fatalf("crypto detail = %#v, want crypto-unavailable backend detail", detail)
	}
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before crypto-unavailable error", sender.sentMetadata, len(sender.chunks))
	}

	transit.SetUnavailable(false)
	transit.SetMissingKey("transit/backend", true)
	sender = &recordingReadSender{}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	requireCryptoUnavailableDetail(t, err)
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before missing-key error", sender.sentMetadata, len(sender.chunks))
	}

	transit.SetMissingKey("transit/backend", false)
	sender = &recordingReadSender{}
	if err := app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read document with key material restored: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_BACKEND {
		t.Fatalf("read source = %s, want backend", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read bytes = %q, want %q", got, data)
	}
}

func TestRunQueuedOperationsOnceRewrapsEnvelopeAndAudits(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("rewrap backend bytes")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	transit := cryptoenv.NewFakeTransit(map[string]uint32{
		"transit/backend-v1": 1,
		"transit/backend-v2": 2,
	})
	app.SetEnvelopeTransit(transit, "transit/backend-v1")
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
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	before, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(t, ctx, backendStore, intent.EnvelopeObjectKey))
	if err != nil {
		t.Fatalf("unmarshal before envelope: %v", err)
	}
	operation := queuedOperation("rewrap-op-1", "rewrap", []*adminv1.Target{
		{
			Target: &adminv1.Target_Block{
				Block: &adminv1.BlockTarget{ShardId: "local", BlockId: stored.Location.BlockID},
			},
		},
	})
	operation.Metadata = map[string]string{"scrap.destination_key_id": "transit/backend-v2"}
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one rewrap success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["envelopes_rewrapped"] != "1" {
		t.Fatalf("finished operation = %#v, want one rewrapped envelope", finished)
	}
	after, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(t, ctx, backendStore, intent.EnvelopeObjectKey))
	if err != nil {
		t.Fatalf("unmarshal after envelope: %v", err)
	}
	if after.GetKeyId() != "transit/backend-v2" ||
		after.GetKeyVersion() != 2 ||
		bytes.Equal(after.GetWrappedDek(), before.GetWrappedDek()) {
		t.Fatalf("after envelope = %#v, want rewrapped key material", after)
	}
	events, err := store.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 ||
		events[0].GetEventType() != "rewrap_completed" ||
		events[0].GetOperationType() != "rewrap" ||
		events[0].GetActorIdentity() != "test" {
		t.Fatalf("audit events = %#v, want rewrap completion event", events)
	}

	if err := store.Put(operation); err != nil {
		t.Fatalf("put duplicate operation: %v", err)
	}
	result, err = app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run duplicate queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("duplicate operation result = %#v, want idempotent success", result)
	}
	duplicateFinished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get duplicate operation: %v", err)
	}
	if duplicateFinished.GetProgress().GetCounters()["envelopes_rewrapped"] != "0" ||
		duplicateFinished.GetProgress().GetCounters()["envelopes_skipped"] != "1" {
		t.Fatalf("duplicate finished operation = %#v, want skipped no-op rewrap", duplicateFinished)
	}
	duplicate, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(t, ctx, backendStore, intent.EnvelopeObjectKey))
	if err != nil {
		t.Fatalf("unmarshal duplicate envelope: %v", err)
	}
	if !bytes.Equal(duplicate.GetWrappedDek(), after.GetWrappedDek()) ||
		!bytes.Equal(duplicate.GetEnvelopeSha256(), after.GetEnvelopeSha256()) {
		t.Fatalf("duplicate envelope = %#v, want unchanged %#v", duplicate, after)
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

func TestHeadDocumentReportsColdMetadataWithoutLocalBytes(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("cold metadata is still visible")
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
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "head-cold-1", time.Unix(210, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
	}
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}

	metadata, err := app.HeadDocument(ctx, api.HeadDocumentRequest{Identity: doc})
	if err != nil {
		t.Fatalf("head cold document: %v", err)
	}
	if metadata.Availability != scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_COLD ||
		metadata.Length != uint64(len(data)) ||
		metadata.Identity != doc {
		t.Fatalf("metadata = %#v, want cold metadata from metastore", metadata)
	}
}

func TestReadDocumentQueuesRestoreOnColdReadAndRetriesAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := OpenWithContext(ctx, dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	store, err := operations.Open(dir)
	if err != nil {
		t.Fatalf("open operation store: %v", err)
	}
	data := []byte("restore queued by read")
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
	app.SetOperationStore(store)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "read-cold-1", time.Unix(220, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
	}
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}

	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	if !detail.GetRestoreQueued() || detail.GetRestoreState() != "restore_pending" {
		t.Fatalf("restore detail = %#v, want queued restore pending", detail)
	}
	operationID := restoreOnReadOperationID(stored)
	queued, err := store.Get(operationID)
	if err != nil {
		t.Fatalf("get queued restore-on-read operation: %v", err)
	}
	if queued.GetOperationType() != "restore" ||
		queued.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		queued.GetMetadata()[operationLaneMetadata] != operationLaneInteractive ||
		queued.GetMetadata()[backendLaneMetadata] != string(backend.LaneRestore) {
		t.Fatalf("queued operation = %#v, want restore-on-read lane metadata", queued)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close operation store: %v", err)
	}

	reopened, err := OpenWithContext(ctx, dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	reopened.SetBackendStore(backendStore)
	reopenedStore, err := operations.Open(dir)
	if err != nil {
		t.Fatalf("reopen operation store: %v", err)
	}
	defer reopenedStore.Close()
	reopened.SetOperationStore(reopenedStore)
	result, err := reopened.RunQueuedOperationsOnce(ctx, reopenedStore)
	if err != nil {
		t.Fatalf("run queued restore after restart: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Pending != 0 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want retry restore success after restart", result)
	}
	events, err := reopenedStore.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	eventTypes := make(map[string]bool, len(events))
	for _, event := range events {
		eventTypes[event.GetEventType()] = true
	}
	if len(events) != 2 ||
		!eventTypes[operationEventRestoreQueued] ||
		!eventTypes[operationEventRestoreComplete] {
		t.Fatalf("audit events = %#v, want queued and completed restore events", events)
	}
	sender := &recordingReadSender{}
	if err := reopened.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read restored document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want restored local", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("restored bytes = %q, want %q", got, data)
	}
}

func TestReadDocumentRequeuesTerminalRestoreOnReadOperation(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	app.SetOperationStore(store)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("terminal restore retry")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "terminal-cold-1", time.Unix(225, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	operationID := restoreOnReadOperationID(stored)
	terminal := queuedOperation(operationID, "restore", []*adminv1.Target{documentTarget(doc)})
	terminal.State = adminv1.OperationState_OPERATION_STATE_FAILED
	terminal.FinishedAt = timestamppb.New(time.Unix(226, 0).UTC())
	terminal.LastError = &adminv1.OperationError{Code: "SCRAP_RESTORE_FAILED", Message: "previous restore failed"}
	if err := store.Put(terminal); err != nil {
		t.Fatalf("put terminal operation: %v", err)
	}

	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	if !detail.GetRestoreQueued() {
		t.Fatalf("restore detail = %#v, want queued retry", detail)
	}
	requeued, err := store.Get(operationID)
	if err != nil {
		t.Fatalf("get requeued operation: %v", err)
	}
	if requeued.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		requeued.GetFinishedAt() != nil ||
		requeued.GetLastError() != nil ||
		requeued.GetProgress().GetMessage() != "requeued by cold read" {
		t.Fatalf("requeued operation = %#v, want queued clean retry", requeued)
	}
	events, err := store.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 || events[0].GetEventType() != operationEventRestoreQueued {
		t.Fatalf("audit events = %#v, want queued audit backfill", events)
	}
}

func TestRunQueuedOperationsOnceKeepsArchiveRestorePendingAndRetries(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("archive restore pending")
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
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "archive-cold-1", time.Unix(230, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
	}
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}
	archive := &archivePendingBackend{Store: backendStore, pending: true}
	app.SetBackendStore(archive)
	operation := queuedOperation("archive-restore-op-1", "restore", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run archive-pending restore: %v", err)
	}
	if result.Scanned != 1 || result.Pending != 1 || result.Succeeded != 0 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want queued pending restore", result)
	}
	pending, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get pending operation: %v", err)
	}
	if pending.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		pending.GetProgress().GetCounters()["blocks_pending"] != "1" {
		t.Fatalf("pending operation = %#v, want queued archive pending", pending)
	}
	cold, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head pending document: %v", err)
	}
	if cold.RestoreState != metastore.RestoreStateRestorePending ||
		cold.Availability != metastore.AvailabilityRestorePending {
		t.Fatalf("restore state = %d/%d, want restore pending", cold.RestoreState, cold.Availability)
	}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	requireRestorePendingDetail(t, err)

	archive.pending = false
	result, err = app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("retry archive restore: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Pending != 0 || result.Failed != 0 {
		t.Fatalf("retry result = %#v, want restore success", result)
	}
}

func TestRunQueuedOperationsOncePrewarmsDocumentFromBackendAndAudits(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("prewarm me from backend")
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
	if err := app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "prewarm-cold-1", time.Unix(240, 0).UTC()); err != nil {
		t.Fatalf("mark cold: %v", err)
	}
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove local block: %v", err)
	}
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}
	operation := queuedOperation("prewarm-op-1", "prewarm", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued prewarm: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one prewarm success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetProgress().GetCounters()["blocks_restored"] != "1" ||
		finished.GetProgress().GetCounters()["operation_lane"] != operationLanePlannedPrewarm ||
		finished.GetProgress().GetCounters()["backend_lane"] != string(backend.LaneRestore) {
		t.Fatalf("finished operation = %#v, want prewarm restore counters", finished)
	}
	events, err := store.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 ||
		events[0].GetEventType() != operationEventPrewarmComplete ||
		events[0].GetOperationType() != "prewarm" {
		t.Fatalf("audit events = %#v, want prewarm completed event", events)
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

func TestRunQueuedOperationsOnceRepairsQuarantinedLocalBlockFromPeer(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("repair me from peer")
	doc := testDocumentIdentity()
	doc.DocumentName = "peer-repair.xml"
	peer := newRecordingPreparePeer("member-1")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	app.peerPrepareTargets = []replication.Target{{MemberID: "member-1", Preparer: peer}}
	app.SetPeerRepairSource("member-1", peer)
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
		t.Fatalf("head stored document: %v", err)
	}
	corruptStoredByte(t, app, stored.Location, 0)
	if err := app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(270, 0).UTC()); err != nil {
		t.Fatalf("record local repair state: %v", err)
	}
	operation := queuedOperation("peer-repair-op-1", "repair", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one peer repair success", result)
	}
	if peer.repairReadCount != 1 {
		t.Fatalf("peer repair reads = %d, want 1", peer.repairReadCount)
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

func TestRunQueuedOperationsOnceQuarantinesCorruptPeerAndFailsWithoutVerifiedSource(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("corrupt peer repair")
	doc := testDocumentIdentity()
	doc.DocumentName = "corrupt-peer-repair.xml"
	peer := newRecordingPreparePeer("member-1")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	app.peerPrepareTargets = []replication.Target{{MemberID: "member-1", Preparer: peer}}
	app.SetPeerRepairSource("member-1", peer)
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
		t.Fatalf("head stored document: %v", err)
	}
	peer.repairBytes = append([]byte(nil), peer.preparedBytes...)
	peer.repairBytes[0] ^= 0xff
	corruptStoredByte(t, app, stored.Location, 0)
	if err := app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(280, 0).UTC()); err != nil {
		t.Fatalf("record local repair state: %v", err)
	}
	operation := queuedOperation("peer-repair-op-2", "repair", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed repair", result)
	}
	if peer.repairReadCount != 1 {
		t.Fatalf("peer repair reads = %d, want 1", peer.repairReadCount)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 2 ||
		!repairQueueReasonContains(queue, "local/"+stored.Location.BlockID) ||
		!repairQueueReasonContains(queue, "peer/member-1/"+stored.Location.BlockID) {
		t.Fatalf("repair queue = %#v, want local and peer quarantines", queue)
	}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.DataLoss)
}

func TestRunQueuedOperationsOnceRetriesPeerRepairAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := Open(dir)
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	store := openTestOperationStore(t)
	data := []byte("retry repair after restart")
	doc := testDocumentIdentity()
	doc.DocumentName = "restart-peer-repair.xml"
	peer := newRecordingPreparePeer("member-1")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	app.peerPrepareTargets = []replication.Target{{MemberID: "member-1", Preparer: peer}}
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		_ = app.Close()
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		_ = app.Close()
		t.Fatalf("head stored document: %v", err)
	}
	corruptStoredByte(t, app, stored.Location, 0)
	if err := app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(290, 0).UTC()); err != nil {
		_ = app.Close()
		t.Fatalf("record local repair state: %v", err)
	}
	first := queuedOperation("restart-peer-repair-op-1", "repair", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(first); err != nil {
		_ = app.Close()
		t.Fatalf("put first operation: %v", err)
	}
	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		_ = app.Close()
		t.Fatalf("run first queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		_ = app.Close()
		t.Fatalf("first operation result = %#v, want failed repair", result)
	}
	peerBytes := append([]byte(nil), peer.preparedBytes...)
	if err := app.Close(); err != nil {
		t.Fatalf("close app: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen app: %v", err)
	}
	defer reopened.Close()
	reopened.SetPeerRepairSource("member-1", PeerRepairSourceFunc(func(ctx context.Context, replica blockstore.ReplicaRef, writer io.Writer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := writer.Write(peerBytes)
		return err
	}))
	second := queuedOperation("restart-peer-repair-op-2", "repair", []*adminv1.Target{documentTarget(doc)})
	if err := store.Put(second); err != nil {
		t.Fatalf("put second operation: %v", err)
	}

	result, err = reopened.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run second queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("second operation result = %#v, want successful retry", result)
	}
	queue, err := reopened.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("repair queue = %#v, want resolved retry", queue)
	}
	sender := &recordingReadSender{}
	if err := reopened.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read repaired document: %v", err)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("repaired bytes = %q, want %q", got, data)
	}
}

func TestRunQueuedOperationsOnceDedupesBackendRepairByBlock(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	first := testDocumentIdentity()
	first.DocumentName = "first-backend-dedupe.xml"
	second := testDocumentIdentity()
	second.DocumentName = "second-backend-dedupe.xml"
	firstData := []byte("first repair from backend")
	secondData := []byte("second repair from backend")
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         first,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{firstData})); err != nil {
		t.Fatalf("write first document: %v", err)
	}
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         second,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{secondData})); err != nil {
		t.Fatalf("write second document: %v", err)
	}
	firstStored, err := app.metadata.HeadDocument(first)
	if err != nil {
		t.Fatalf("head first document: %v", err)
	}
	secondStored, err := app.metadata.HeadDocument(second)
	if err != nil {
		t.Fatalf("head second document: %v", err)
	}
	if firstStored.Location.BlockID != secondStored.Location.BlockID {
		t.Fatalf("documents landed in blocks %s/%s, want same block", firstStored.Location.BlockID, secondStored.Location.BlockID)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	app.sealBlockAtBytes = app.blocks.CurrentBlockLength()
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	intent, err := app.metadata.GetUploadIntent(firstStored.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	countingBackend := &readCountingBackendStore{Store: backendStore}
	app.SetBackendStore(countingBackend)
	corruptStoredByte(t, app, firstStored.Location, 0)
	for _, stored := range []metastore.Document{firstStored, secondStored} {
		if err := app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(300, 0).UTC()); err != nil {
			t.Fatalf("record local repair state for %s: %v", stored.Identity.DocumentName, err)
		}
	}
	operation := queuedOperation("backend-dedupe-repair-op", "repair", []*adminv1.Target{
		{
			Target: &adminv1.Target_Block{
				Block: &adminv1.BlockTarget{
					ShardId: "local",
					BlockId: firstStored.Location.BlockID,
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
	if countingBackend.readObjectRangeCount(intent.BackendObjectKey) != 1 {
		t.Fatalf("block backend reads = %d, want one block restore", countingBackend.readObjectRangeCount(intent.BackendObjectKey))
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("repair queue = %#v, want resolved repairs", queue)
	}
}

func TestRepairDocumentFromVerifiedPeerPropagatesContextCancellation(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("cancelled peer repair")
	doc := testDocumentIdentity()
	doc.DocumentName = "cancel-peer-repair.xml"
	peer := newRecordingPreparePeer("member-1")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	app.peerPrepareTargets = []replication.Target{{MemberID: "member-1", Preparer: peer}}
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
		t.Fatalf("head stored document: %v", err)
	}
	app.SetPeerRepairSource("member-1", PeerRepairSourceFunc(func(context.Context, blockstore.ReplicaRef, io.Writer) error {
		return context.Canceled
	}))

	repaired, err := app.repairDocumentFromVerifiedPeer(ctx, stored, time.Unix(310, 0).UTC())
	if repaired || !errors.Is(err, context.Canceled) {
		t.Fatalf("peer repair result = %t, %v, want context cancellation", repaired, err)
	}
}

func TestRunQueuedOperationsOnceScrubQueuesRepairForCorruptLocalBlock(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(260, 0).UTC())
	store := openTestOperationStore(t)
	data := []byte("scrub detects corruption")
	doc := testDocumentIdentity()
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
	operation := queuedOperation("scrub-op-1", "scrub", []*adminv1.Target{
		{
			Target: &adminv1.Target_Shard{
				Shard: &adminv1.ShardTarget{ShardId: "local"},
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
		t.Fatalf("operation result = %#v, want one successful scrub", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["documents_scanned"] != "1" ||
		finished.GetProgress().GetCounters()["repair_queued"] != "1" {
		t.Fatalf("finished operation = %#v, want scrub repair counter", finished)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 1 ||
		queue[0].GetTarget().GetDocument().GetDocumentName() != doc.DocumentName ||
		queue[0].GetDetectedAt().AsTime() != time.Unix(260, 0).UTC() {
		t.Fatalf("repair queue = %#v, want scrub-queued repair", queue)
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
	stored := writeLocalReadVerificationDocument(t, app, doc, data)
	corruptStoredByte(t, app, stored.Location, 0)

	sender := &recordingReadSender{}
	err := app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender)
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

func TestCleanFullReadReturnsVerifiedLocalBytes(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("clean full read bytes")
	writeLocalReadVerificationDocument(t, app, doc, data)

	sender := &recordingReadSender{}
	if err := app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender); err != nil {
		t.Fatalf("read document: %v", err)
	}
	if !sender.sentMetadata {
		t.Fatal("read did not send metadata")
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want local", sender.metadata.Source)
	}
	if sender.metadata.SelectedRange.Offset != 0 ||
		sender.metadata.SelectedRange.Length == nil ||
		*sender.metadata.SelectedRange.Length != uint64(len(data)) {
		t.Fatalf("selected range = %#v, want full document", sender.metadata.SelectedRange)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read bytes = %q, want %q", got, data)
	}
}

func TestRangedReadVerifiesEveryTouchedFrameBeforeStreaming(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := bytes.Repeat([]byte("a"), int(blockstore.DefaultFrameSize)+8)
	crossFrameOffset := uint64(blockstore.DefaultFrameSize - 3)
	readLength := uint64(6)
	copy(data[int(crossFrameOffset):int(crossFrameOffset+readLength)], []byte("XYZ123"))
	stored := writeLocalReadVerificationDocument(t, app, doc, data)

	sender := &recordingReadSender{}
	err := app.ReadDocument(context.Background(), api.ReadDocumentRequest{
		Identity: doc,
		Range: &api.ReadRange{
			Offset: crossFrameOffset,
			Length: &readLength,
		},
	}, sender)
	if err != nil {
		t.Fatalf("read ranged document: %v", err)
	}
	if sender.metadata.Source != scrapv1.StorageSource_STORAGE_SOURCE_LOCAL {
		t.Fatalf("read source = %s, want local", sender.metadata.Source)
	}
	if sender.metadata.SelectedRange.Offset != crossFrameOffset ||
		sender.metadata.SelectedRange.Length == nil ||
		*sender.metadata.SelectedRange.Length != readLength {
		t.Fatalf("selected range = %#v, want cross-frame range", sender.metadata.SelectedRange)
	}
	expected := data[int(crossFrameOffset):int(crossFrameOffset+readLength)]
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, expected) {
		t.Fatalf("ranged bytes = %q, want %q", got, expected)
	}

	corruptStoredByte(t, app, stored.Location, crossFrameOffset+4)
	sender = &recordingReadSender{}
	err = app.ReadDocument(context.Background(), api.ReadDocumentRequest{
		Identity: doc,
		Range: &api.ReadRange{
			Offset: crossFrameOffset,
			Length: &readLength,
		},
	}, sender)
	requireCode(t, err, codes.DataLoss)
	requireIntegrityDetail(t, err)
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before ranged verification failure", sender.sentMetadata, len(sender.chunks))
	}
}

func TestMissingLocalBytesFailsBeforeSendingMetadata(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("missing local read bytes")
	stored := writeLocalReadVerificationDocument(t, app, doc, data)
	if err := os.Remove(app.blocks.BlockPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("remove block: %v", err)
	}

	sender := &recordingReadSender{}
	err := app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.DataLoss)
	detail := requireIntegrityDetail(t, err)
	if detail.GetIdentity().GetDocumentName() != doc.DocumentName ||
		len(detail.GetAttemptedSources()) != 1 ||
		detail.GetAttemptedSources()[0] != "local" {
		t.Fatalf("integrity detail = %#v, want local source", detail)
	}
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d before missing-ref failure", sender.sentMetadata, len(sender.chunks))
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
	appliedIndex := app.authority.AppliedIndex()
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
	if app.authority.AppliedIndex() != appliedIndex {
		t.Fatalf("applied index = %d, want replay not to append after %d", app.authority.AppliedIndex(), appliedIndex)
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
	stored, err := app.metadata.HeadDocument(testDocumentIdentity())
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
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
	required := publication.Manifest.GetRequiredObjects()
	if !hasRequiredObject(required, intent.BackendObjectKey) ||
		!hasRequiredObject(required, intent.IndexObjectKey) ||
		!hasRequiredObject(required, intent.EnvelopeObjectKey) {
		t.Fatalf("required objects = %#v, want block/index/envelope refs", required)
	}

	readiness, err := app.GetRecoveryReadiness(ctx)
	if err != nil {
		t.Fatalf("get recovery readiness: %v", err)
	}
	if !readiness.GetReady() || readiness.GetLatestRestorableCheckpointAt().AsTime() != app.now() ||
		!hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_NON_PRODUCTION_MODE") ||
		!hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_MEASURED_EVIDENCE_ONLY") ||
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
		finished.GetProgress().GetCounters()["verified_objects"] == "" ||
		finished.GetProgress().GetCounters()["verified_block_objects"] != "1" ||
		finished.GetProgress().GetCounters()["verified_index_objects"] != "1" ||
		finished.GetProgress().GetCounters()["verified_envelope_objects"] != "1" ||
		finished.GetProgress().GetCounters()["recovery_report_kind"] != recoveryEvidenceReportKind ||
		finished.GetProgress().GetCounters()["rto_promise"] != recoveryNoFormalPromise ||
		finished.GetProgress().GetCounters()["rpo_promise"] != recoveryNoFormalPromise {
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
	completedAt := time.Unix(710, 0).UTC()
	source.now = fixedClock(completedAt)
	if _, err := source.CompleteTransaction(ctx, api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags:        map[string]string{"closed_by": "metadata-restore-test"},
	}); err != nil {
		t.Fatalf("complete source transaction: %v", err)
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
		finished.GetProgress().GetCounters()["transactions"] != "1" ||
		finished.GetProgress().GetCounters()["upload_intents"] != "1" {
		t.Fatalf("finished operation = %#v, want imported document, transaction, and upload intent", finished)
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
	if intent.State != metastore.UploadStateUploaded ||
		intent.BackendObjectKey == "" ||
		intent.IndexObjectKey == "" ||
		intent.EnvelopeObjectKey == "" {
		t.Fatalf("restored intent = %#v, want uploaded backend/index/envelope objects", intent)
	}
	transaction, err := restoredApp.metadata.GetTransaction(identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID})
	if err != nil {
		t.Fatalf("get restored transaction: %v", err)
	}
	if transaction.State != metastore.TransactionStateCompleted ||
		transaction.CompletedAt == nil ||
		!transaction.CompletedAt.Equal(completedAt) ||
		transaction.Tags["closed_by"] != "metadata-restore-test" {
		t.Fatalf("restored transaction = %#v, want completed transaction state", transaction)
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
		finished.GetProgress().GetCounters()["upload_intents"] != "1" ||
		finished.GetProgress().GetCounters()["blocks_restored"] != "0" ||
		finished.GetProgress().GetCounters()["recovery_report_kind"] != recoveryEvidenceReportKind {
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
		finished.GetProgress().GetCounters()["upload_intents"] != "1" ||
		finished.GetProgress().GetCounters()["blocks_restored"] != "1" ||
		finished.GetProgress().GetCounters()["verified_block_objects"] != "1" ||
		finished.GetProgress().GetCounters()["verified_index_objects"] != "1" ||
		finished.GetProgress().GetCounters()["verified_envelope_objects"] != "1" ||
		finished.GetProgress().GetCounters()["recovery_report_kind"] != recoveryEvidenceReportKind ||
		finished.GetProgress().GetCounters()["rto_promise"] != recoveryNoFormalPromise ||
		finished.GetProgress().GetCounters()["rpo_promise"] != recoveryNoFormalPromise {
		t.Fatalf("finished operation = %#v, want scratch drill restore counters", finished)
	}
}

func TestRunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactMissing(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(802, 0).UTC())
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	publication, _, intent := publishDRDrillTestDocument(t, ctx, app, backendStore, []byte("missing artifact drill bytes"))
	app.SetBackendStore(&faultingBackendStore{
		Store:       backendStore,
		missingHead: map[string]bool{intent.EnvelopeObjectKey: true},
	})

	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-missing-artifact-1", "dr-drill", []*adminv1.Target{
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
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DR_DRILL_FAILED" ||
		!strings.Contains(finished.GetLastError().GetMessage(), backend.ErrNotFound.Error()) {
		t.Fatalf("finished operation = %#v, want missing required artifact failure", finished)
	}
}

func TestRunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactCorrupt(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(803, 0).UTC())
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	publication, _, intent := publishDRDrillTestDocument(t, ctx, app, backendStore, []byte("corrupt artifact drill bytes"))
	app.SetBackendStore(&faultingBackendStore{
		Store:      backendStore,
		readErrors: map[string]error{intent.IndexObjectKey: backend.ErrChecksumMismatch},
	})

	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-corrupt-artifact-1", "dr-drill", []*adminv1.Target{
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
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DR_DRILL_FAILED" ||
		!strings.Contains(finished.GetLastError().GetMessage(), backend.ErrChecksumMismatch.Error()) {
		t.Fatalf("finished operation = %#v, want corrupt required artifact failure", finished)
	}
}

func TestRunQueuedOperationsOnceDRDrillFailsWhenKeyMaterialMissing(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(804, 0).UTC())
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 2})
	app.SetEnvelopeTransit(transit, "transit/backend")
	backendStore, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	publication, _, _ := publishDRDrillTestDocument(t, ctx, app, backendStore, []byte("missing key material drill bytes"))
	transit.SetMissingKey("transit/backend", true)

	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-missing-key-1", "dr-drill", []*adminv1.Target{
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
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DR_DRILL_FAILED" ||
		!strings.Contains(finished.GetLastError().GetMessage(), cryptoenv.ErrKeyMaterialUnavailable.Error()) {
		t.Fatalf("finished operation = %#v, want missing key material failure", finished)
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

func TestRunQueuedOperationsOnceCapacityOverrideRecordsBoundedEvidence(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	operation := queuedOperation("capacity-override-op-1", "capacity-override", []*adminv1.Target{
		{
			Target: &adminv1.Target_CapacityProfile{
				CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
			},
		},
	})
	operation.DryRun = true
	operation.Metadata = map[string]string{
		"scrap.capacity_profile_id":          "production-a",
		"scrap.capacity_override_expires_at": "2026-05-23T13:00:00Z",
		"scrap.capacity_override_reason":     "incident INC-42",
	}
	if err := store.Put(operation); err != nil {
		t.Fatalf("put operation: %v", err)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one successful capacity override dry-run", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetLastError() != nil ||
		finished.GetProgress().GetCounters()["capacity_profile_id"] != "production-a" ||
		finished.GetProgress().GetCounters()["reason"] != "incident INC-42" ||
		!hasWarningCode(finished.GetWarnings(), "SCRAP_CAPACITY_OVERRIDE_RECORDED_ONLY") {
		t.Fatalf("finished operation = %#v, want recorded bounded capacity override evidence", finished)
	}
	events, err := store.ListAuditEvents()
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	if len(events) != 1 ||
		events[0].GetEventType() != "capacity_override_completed" ||
		events[0].GetOperationType() != "capacity-override" ||
		events[0].GetMetadata()["scrap.capacity_override_reason"] != "incident INC-42" {
		t.Fatalf("audit events = %#v, want capacity override completion evidence", events)
	}
}

func TestRecoverInterruptedOperationsRequeuesRunningCapacityOverrideAfterRestart(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(500, 0).UTC())
	storeDir := t.TempDir()
	store, err := operations.Open(storeDir)
	if err != nil {
		t.Fatalf("open operation store: %v", err)
	}
	operation := queuedOperation("capacity-override-restart-op-1", "capacity-override", []*adminv1.Target{
		{
			Target: &adminv1.Target_CapacityProfile{
				CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: "production-a"},
			},
		},
	})
	operation.State = adminv1.OperationState_OPERATION_STATE_RUNNING
	operation.StartedAt = timestamppb.New(time.Unix(400, 0).UTC())
	operation.DryRun = true
	operation.Metadata = map[string]string{
		"scrap.capacity_profile_id":          "production-a",
		"scrap.capacity_override_expires_at": "2026-05-23T13:00:00Z",
		"scrap.capacity_override_reason":     "incident INC-42",
		"audit_correlation_id":               "audit-1",
	}
	operation.Progress = &adminv1.OperationProgress{
		Message: "retrying capacity override evidence",
		Counters: map[string]string{
			"retry_attempt": "2",
		},
	}
	operation.Warnings = []*adminv1.OperationWarning{
		{Code: "SCRAP_CAPACITY_OVERRIDE_RETRY", Message: "previous attempt was interrupted"},
	}
	operation.LastError = &adminv1.OperationError{Code: "SCRAP_CAPACITY_OVERRIDE_RETRYABLE", Message: "process stopped"}
	if err := store.Put(operation); err != nil {
		_ = store.Close()
		t.Fatalf("put operation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close operation store: %v", err)
	}

	reopenedStore, err := operations.Open(storeDir)
	if err != nil {
		t.Fatalf("reopen operation store: %v", err)
	}
	defer reopenedStore.Close()
	recovery, err := app.RecoverInterruptedOperations(ctx, reopenedStore)
	if err != nil {
		t.Fatalf("recover interrupted operations: %v", err)
	}
	if recovery.Scanned != 1 || recovery.Requeued != 1 || recovery.FailedUnsupported != 0 {
		t.Fatalf("recovery result = %#v, want one requeued running operation", recovery)
	}
	recovered, err := reopenedStore.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get recovered operation: %v", err)
	}
	if recovered.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		recovered.GetProgress().GetCounters()["retry_attempt"] != "2" ||
		recovered.GetLastError().GetCode() != "SCRAP_CAPACITY_OVERRIDE_RETRYABLE" ||
		recovered.GetMetadata()["audit_correlation_id"] != "audit-1" ||
		!hasWarningCode(recovered.GetWarnings(), "SCRAP_CAPACITY_OVERRIDE_RETRY") ||
		!hasWarningCode(recovered.GetWarnings(), "SCRAP_OPERATION_RESTART_REQUEUED") {
		t.Fatalf("recovered operation = %#v, want queued retry with restart evidence preserved", recovered)
	}

	result, err := app.RunQueuedOperationsOnce(ctx, reopenedStore)
	if err != nil {
		t.Fatalf("run queued operations: %v", err)
	}
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want recovered capacity override to succeed", result)
	}
	finished, err := reopenedStore.Get(operation.GetOperationId())
	if err != nil {
		t.Fatalf("get finished operation: %v", err)
	}
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetCounters()["capacity_profile_id"] != "production-a" ||
		finished.GetProgress().GetCounters()["reason"] != "incident INC-42" ||
		!hasWarningCode(finished.GetWarnings(), "SCRAP_CAPACITY_OVERRIDE_RECORDED_ONLY") {
		t.Fatalf("finished operation = %#v, want recovered capacity override evidence recorded", finished)
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

type archivePendingBackend struct {
	backend.Store
	pending bool
}

func (s *archivePendingBackend) ReadObjectRange(ctx context.Context, key string, selected backend.Range, writer io.Writer) error {
	if s.pending {
		return backend.ErrRestorePending
	}
	return s.Store.ReadObjectRange(ctx, key, selected, writer)
}

type faultingBackendStore struct {
	backend.Store
	missingHead map[string]bool
	readErrors  map[string]error
}

func (s *faultingBackendStore) HeadObject(ctx context.Context, key string) (backend.Object, error) {
	if s.missingHead[key] {
		return backend.Object{}, backend.ErrNotFound
	}
	return s.Store.HeadObject(ctx, key)
}

func (s *faultingBackendStore) ReadObjectRange(ctx context.Context, key string, selected backend.Range, writer io.Writer) error {
	if err := s.readErrors[key]; err != nil {
		return err
	}
	return s.Store.ReadObjectRange(ctx, key, selected, writer)
}

func queuedTombstoneOperation(operationID string, targets []*adminv1.Target) *adminv1.Operation {
	return queuedOperation(operationID, "tombstone", targets)
}

func queuedOperation(operationID, operationType string, targets []*adminv1.Target) *adminv1.Operation {
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

func documentTarget(doc identity.Document) *adminv1.Target {
	return &adminv1.Target{
		Target: &adminv1.Target_Document{
			Document: &adminv1.DocumentTarget{
				TenantId:      doc.TenantID,
				TransactionId: doc.TransactionID,
				DocumentName:  doc.DocumentName,
			},
		},
	}
}

var errSimulatedLocalCrash = errors.New("simulated local crash")

func writeInitForCrashBoundary(doc identity.Document, data []byte, idempotencyKey string) api.WriteDocumentInit {
	length := uint64(len(data))
	sum := sha256.Sum256(data)
	return api.WriteDocumentInit{
		Identity:             doc,
		DocumentClass:        scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:        scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ExpectedLength:       &length,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: idempotencyKey,
		CreatedByService:     "billing-etl",
	}
}

func appendPrepareLogTail(t *testing.T, dir string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(dir, prepareLogName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open prepare log for tail append: %v", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatalf("append prepare log tail: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync prepare log tail: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close prepare log tail: %v", err)
	}
}

func writeLocalReadVerificationDocument(t *testing.T, app *Application, doc identity.Document, data []byte) metastore.Document {
	t.Helper()
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
	return stored
}

func corruptStoredByte(t *testing.T, app *Application, record blockstore.Record, offset uint64) {
	t.Helper()
	file, err := os.OpenFile(app.blocks.BlockPath(record.BlockID), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open block: %v", err)
	}
	storedOffset := int64(record.StoredOffset + offset)
	buf := []byte{0}
	if _, err := file.ReadAt(buf, storedOffset); err != nil {
		_ = file.Close()
		t.Fatalf("read block byte: %v", err)
	}
	buf[0] ^= 0xff
	if _, err := file.WriteAt(buf, storedOffset); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close block: %v", err)
	}
}

func writeDocumentForProjectionRebuild(t *testing.T, dir string, doc identity.Document, data []byte) (*Application, metastore.Document) {
	t.Helper()
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
		_ = app.Close()
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		_ = app.Close()
		t.Fatalf("head stored document: %v", err)
	}
	return app, stored
}

func assertUnreadableRepairRef(t *testing.T, app *Application, doc identity.Document, blockID string) {
	t.Helper()
	sender := &recordingReadSender{}
	err := app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.DataLoss)
	if sender.sentMetadata || len(sender.chunks) != 0 {
		t.Fatalf("sent metadata=%v chunks=%d for unreadable repair ref", sender.sentMetadata, len(sender.chunks))
	}
	queue, err := app.GetRepairQueue(context.Background(), "local")
	if err != nil {
		t.Fatalf("get repair queue: %v", err)
	}
	if len(queue) != 1 ||
		queue[0].GetTarget().GetDocument().GetDocumentName() != doc.DocumentName ||
		!strings.Contains(queue[0].GetReason(), blockID) {
		t.Fatalf("repair queue = %#v, want repair for block %s", queue, blockID)
	}
}

func repairQueueReasonContains(queue []*adminv1.RepairQueueItem, want string) bool {
	for _, item := range queue {
		if strings.Contains(item.GetReason(), want) {
			return true
		}
	}
	return false
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

type recordingPreparePeer struct {
	memberID        string
	prepareCount    int
	repairReadCount int
	preparedBytes   []byte
	repairBytes     []byte
	repairErr       error
	errAfterRead    error
	beforeReceipt   func(replication.PrepareRequest)
}

func newRecordingPreparePeer(memberID string) *recordingPreparePeer {
	return &recordingPreparePeer{memberID: memberID}
}

func (p *recordingPreparePeer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	p.prepareCount++
	var buf bytes.Buffer
	if err := request.WriteBytes(ctx, &buf); err != nil {
		return replication.Receipt{}, err
	}
	p.preparedBytes = append(p.preparedBytes[:0], buf.Bytes()...)
	if err := replication.ValidatePreparedBytes(request.Document, p.preparedBytes); err != nil {
		return replication.Receipt{}, err
	}
	if p.errAfterRead != nil {
		return replication.Receipt{}, p.errAfterRead
	}
	if p.beforeReceipt != nil {
		p.beforeReceipt(request)
	}
	return replication.ReceiptFromPreparedDocument(p.memberID, request.Document), nil
}

func (p *recordingPreparePeer) ReadReplica(ctx context.Context, replica blockstore.ReplicaRef, writer io.Writer) error {
	p.repairReadCount++
	if err := ctx.Err(); err != nil {
		return err
	}
	data := p.preparedBytes
	if p.repairBytes != nil {
		data = p.repairBytes
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if p.repairErr != nil {
		return p.repairErr
	}
	return ctx.Err()
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

type staticFreshnessChecker struct {
	readErr  error
	writeErr error
}

func (c staticFreshnessChecker) RequireWriteQuorum(ctx context.Context, _ raftmeta.FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.writeErr
}

func (c staticFreshnessChecker) RequireReadIndex(ctx context.Context, _ raftmeta.FreshnessCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.readErr
}

func replaceAuthorityFreshness(t *testing.T, app *Application, checker raftmeta.FreshnessChecker) {
	t.Helper()
	if err := app.authority.Close(); err != nil {
		t.Fatalf("close authority: %v", err)
	}
	authority, err := raftmeta.OpenAuthorityWithOptions(filepath.Join(app.dir, "raftmeta"), "local", app.metadata, raftmeta.AuthorityOptions{
		FreshnessChecker: checker,
	})
	if err != nil {
		t.Fatalf("reopen authority with freshness checker: %v", err)
	}
	app.authority = authority
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

type readCountingBackendStore struct {
	backend.Store
	readObjectRangeCounts map[string]int
}

func (s *readCountingBackendStore) ReadObjectRange(ctx context.Context, key string, selected backend.Range, writer io.Writer) error {
	if s.readObjectRangeCounts == nil {
		s.readObjectRangeCounts = make(map[string]int)
	}
	s.readObjectRangeCounts[key]++
	return s.Store.ReadObjectRange(ctx, key, selected, writer)
}

func (s *readCountingBackendStore) readObjectRangeCount(key string) int {
	return s.readObjectRangeCounts[key]
}

func publishDRDrillTestDocument(t *testing.T, ctx context.Context, app *Application, backendStore backend.MutableStore, data []byte) (published.SnapshotPublication, metastore.Document, metastore.UploadIntent) {
	t.Helper()
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := app.PublishMetadataSnapshot(ctx)
	if err != nil {
		t.Fatalf("publish metadata snapshot: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	if err != nil {
		t.Fatalf("head stored document: %v", err)
	}
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	return publication, stored, intent
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

func hasRequiredObject(objects []*publishedv1.ObjectRef, key string) bool {
	for _, object := range objects {
		if object.GetObjectKey() == key {
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

func requireUnsafeCapacityDetail(t *testing.T, err error) *scrapv1.UnsafeCapacityDetail {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a status error: %v", err)
	}
	for _, detail := range st.Details() {
		if capacity, ok := detail.(*scrapv1.UnsafeCapacityDetail); ok {
			return capacity
		}
	}
	t.Fatalf("status details = %#v, want UnsafeCapacityDetail", st.Details())
	return nil
}
