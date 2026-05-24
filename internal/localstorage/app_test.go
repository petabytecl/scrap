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
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/observe"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/published"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/safeconv"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/storageformat"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestHealthChecksReflectLocalStorageState(t *testing.T) {
	app := openTestApplication(t)
	testutil.RequireNoErrorf(t, app.CheckReadiness(context.Background()), "readiness health")
	testutil.RequireNoErrorf(t, app.CheckLiveness(context.Background()), "liveness health")

	testutil.RequireNoErrorf(t, app.blocks.Close(), "close blockstore")
	testutil.RequireErrorIsf(t, app.CheckLiveness(context.Background()), blockstore.ErrClosed, "liveness after closed blockstore")
}

func TestHealthChecksRejectNilApplication(t *testing.T) {
	var app *Application

	if err := app.CheckReadiness(context.Background()); err == nil {
		t.Fatal("nil application readiness error = nil, want error")
	}
	if err := app.CheckLiveness(context.Background()); err == nil {
		t.Fatal("nil application liveness error = nil, want error")
	}
}

func TestWriteHeadReadFindAndCompleteTransaction(t *testing.T) {
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(100, 0).UTC())
	data := []byte("0123456789")
	sum := sha256.Sum256(data)
	expectedLength := uint64(len(data))
	doc := testDocumentIdentity()

	result, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:             doc,
		DocumentClass:        storageapp.DocumentClassPermanent,
		PriorityClass:        storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, err, "write document")
	requireWriteResult(t, result, expectedLength, sum[:])
	prepared, err := app.prepare.Recover()
	testutil.RequireNoErrorf(t, err, "recover prepare log")
	testutil.RequireEqualf(t, len(prepared), 1, "prepare log record count")
	testutil.RequireEqualf(t, prepared[0].Identity, doc, "prepare log identity")
	storedDocument, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	requirePendingUploadIntent(t, intent, storedDocument.Location.BlockID)

	head, err := app.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head document")
	testutil.RequireEqualf(t, head.Identity, doc, "head identity")
	testutil.RequireEqualf(t, head.ContentType, "application/xml", "head content type")
	testutil.RequireTruef(t, head.HasContentType, "head has_content_type = false")

	readLength := uint64(4)
	sender := &recordingReadSender{}
	err = app.ReadDocument(context.Background(), api.ReadDocumentRequest{
		Identity: doc,
		Range: &api.ReadRange{
			Offset: 3,
			Length: &readLength,
		},
	}, sender)
	testutil.RequireNoErrorf(t, err, "read document")
	requireReadSource(t, sender, storageapp.StorageSourceLocal)
	requireSelectedRange(t, sender.metadata.SelectedRange, 3, 4)
	requireReadBytes(t, sender, []byte("3456"))

	found, err := app.FindDocuments(context.Background(), api.FindDocumentsRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Filter: api.DocumentFilter{
			DocumentNamePrefix:    "invoice",
			HasDocumentNamePrefix: true,
			DocumentClass:         storageapp.DocumentClassPermanent,
			HasDocumentClass:      true,
			Tags: map[string]string{
				"workflow": "billing",
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "find documents")
	testutil.RequireEqualf(t, len(found.Documents), 1, "found document count")
	testutil.RequireEqualf(t, found.Documents[0].Identity, doc, "found document identity")

	transaction, err := app.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	testutil.RequireNoErrorf(t, err, "get transaction")
	testutil.RequireEqualf(t, transaction.DocumentCount, uint32(1), "transaction document count")
	testutil.RequireEqualf(t, transaction.PermanentDocumentCount, uint32(1), "transaction permanent document count")

	completedAt := time.Unix(200, 0).UTC()
	app.now = fixedClock(completedAt)
	transaction, err = app.CompleteTransaction(context.Background(), api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags: map[string]string{
			"closed_by": "test",
		},
	})
	testutil.RequireNoErrorf(t, err, "complete transaction")
	testutil.RequireEqualf(t, transaction.State, storageapp.TransactionStateCompleted, "transaction state")
	testutil.RequireNotNilf(t, transaction.CompletedAt, "completed_at is nil")
	testutil.RequireTruef(t, transaction.CompletedAt.Equal(completedAt), "completed_at = %v, want %v", transaction.CompletedAt, completedAt)
}

func TestCompleteTransactionRetryPreservesOriginalStateAndDoesNotAppend(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("transaction retry bytes")
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")

	completedAt := time.Unix(200, 0).UTC()
	app.now = fixedClock(completedAt)
	first, err := app.CompleteTransaction(ctx, api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags:        map[string]string{"closed_by": "first"},
	})
	testutil.RequireNoErrorf(t, err, "complete transaction")
	firstIndex := app.authority.AppliedIndex()

	app.now = fixedClock(completedAt.Add(time.Hour))
	retry, err := app.CompleteTransaction(ctx, api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags:        map[string]string{"closed_by": "retry"},
	})
	testutil.RequireNoErrorf(t, err, "retry complete transaction")

	testutil.RequireEqualf(t, app.authority.AppliedIndex(), firstIndex, "applied index after retry")
	testutil.RequireDeepEqualf(t, retry.Tags, first.Tags, "retry tags")
	testutil.RequireNotNilf(t, retry.CompletedAt, "retry completed_at")
	testutil.RequireTruef(t, retry.CompletedAt.Equal(completedAt), "retry completed_at = %v, want %v", retry.CompletedAt, completedAt)
}

func requireWriteResult(t *testing.T, result api.WriteDocumentResult, expectedLength uint64, expectedSHA []byte) {
	t.Helper()
	testutil.RequireEqualf(t, result.DesiredReplicaCount, uint32(1), "desired replica count")
	testutil.RequireEqualf(t, result.AchievedReplicaCount, uint32(1), "achieved replica count")
	testutil.RequireEqualf(t, result.Metadata.Length, expectedLength, "write length")
	testutil.RequireTruef(t, bytes.Equal(result.Metadata.LogicalSHA256, expectedSHA), "write logical sha was not returned")
}

func requirePendingUploadIntent(t *testing.T, intent metastore.UploadIntent, blockID string) {
	t.Helper()
	testutil.RequireEqualf(t, intent.State, metastore.UploadStatePending, "upload intent state")
	testutil.RequireEqualf(t, intent.BackendObjectKey, "blocks/"+blockID+".blk", "backend object key")
	testutil.RequireEqualf(t, intent.IndexObjectKey, "blocks/"+blockID+".idx", "index object key")
	testutil.RequireEqualf(t, intent.EnvelopeObjectKey, "blocks/"+blockID+".env", "envelope object key")
}

func TestStoredSHA256EqualsLogicalSHA256WithoutEncryption(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	data := []byte("plaintext stored bytes")
	want := sha256.Sum256(data)

	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")

	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireEqualf(t, stored.LogicalSHA256, want, "logical sha256")
	testutil.RequireEqualf(t, stored.StoredSHA256, want, "stored sha256")
}

func TestStoredSHA256DiffersFromLogicalSHA256WhenEncrypted(t *testing.T) {
	logicalSHA := sha256.Sum256([]byte("client-visible plaintext"))
	storedSHA := sha256.Sum256([]byte("encrypted stored bytes"))

	document := newDocument(api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, blockstore.Record{
		BlockID:       "block-1",
		StoredOffset:  blockstore.HeaderLength,
		StoredLength:  uint64(len("encrypted stored bytes")),
		LogicalSHA256: logicalSHA,
		StoredSHA256:  storedSHA,
	}, time.Unix(100, 0).UTC())

	testutil.RequireEqualf(t, document.LogicalSHA256, logicalSHA, "logical sha256")
	testutil.RequireEqualf(t, document.StoredSHA256, storedSHA, "stored sha256")
	if document.StoredSHA256 == document.LogicalSHA256 {
		t.Fatal("stored sha256 matched logical sha256 for encrypted stored bytes")
	}
}

func requireReadSource(t *testing.T, sender *recordingReadSender, source storageapp.StorageSource) {
	t.Helper()
	testutil.RequireEqualf(t, sender.metadata.Source, source, "read source")
}

func requireSelectedRange(t *testing.T, readRange api.ReadRange, offset, length uint64) {
	t.Helper()
	testutil.RequireEqualf(t, readRange.Offset, offset, "selected range offset")
	testutil.RequireNotNilf(t, readRange.Length, "selected range length")
	testutil.RequireEqualf(t, *readRange.Length, length, "selected range length")
}

func requireReadBytes(t *testing.T, sender *recordingReadSender, want []byte) {
	t.Helper()
	got := bytes.Join(sender.chunks, nil)
	testutil.RequireTruef(t, bytes.Equal(got, want), "read bytes = %q, want %q", got, want)
}

func requireOperationRunResult(t *testing.T, result OperationRunResult, scanned, succeeded, pending, failed int) {
	t.Helper()
	testutil.RequireEqualf(t, result.Scanned, scanned, "operation scanned count")
	testutil.RequireEqualf(t, result.Succeeded, succeeded, "operation succeeded count")
	testutil.RequireEqualf(t, result.Pending, pending, "operation pending count")
	testutil.RequireEqualf(t, result.Failed, failed, "operation failed count")
}

func requireUploadRunResult(t *testing.T, result backendupload.RunResult, scanned, uploaded, failed, deferred int) {
	t.Helper()
	testutil.RequireEqualf(t, result.Scanned, scanned, "upload scanned count")
	testutil.RequireEqualf(t, result.Uploaded, uploaded, "upload uploaded count")
	testutil.RequireEqualf(t, result.Failed, failed, "upload failed count")
	testutil.RequireEqualf(t, result.Deferred, deferred, "upload deferred count")
}

func requireBackendObject(ctx context.Context, t *testing.T, store backend.Store, key string) {
	t.Helper()
	_, err := store.HeadObject(ctx, key)
	testutil.RequireNoErrorf(t, err, "head backend object %s", key)
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	if result.DesiredReplicaCount != 3 || result.AchievedReplicaCount != 3 {
		t.Fatalf("replica counts = %d/%d, want 3/3", result.DesiredReplicaCount, result.AchievedReplicaCount)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
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

func TestWriteDocumentRejectsEmptyChunk(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	_, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{{}}))
	requireCode(t, err, codes.InvalidArgument)
	if _, headErr := app.metadata.HeadDocument(doc); !errors.Is(headErr, metastore.ErrNotFound) {
		t.Fatalf("head after empty chunk write = %v, want %v", headErr, metastore.ErrNotFound)
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, err, "open app")
	doc := testDocumentIdentity()
	badSHA := bytes.Repeat([]byte{9}, 32)

	_, err = app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassEphemeral,
		PriorityClass:    storageapp.PriorityClassNormal,
		ExpectedSHA256:   badSHA,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("real bytes")}))
	requireCode(t, err, codes.InvalidArgument)
	if got := app.blocks.CurrentBlockLength(); got != blockstore.HeaderLength {
		t.Fatalf("open block length = %d, want rejected write rollback to header length %d", got, blockstore.HeaderLength)
	}

	_, err = app.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	_, err = reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	prepared, err := reopened.prepare.Recover()
	testutil.RequireNoErrorf(t, err, "recover prepare log")
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, err, "open app")
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head after reopen")
	if head.Length != uint64(len(data)) {
		t.Fatalf("head length after reopen = %d, want %d", head.Length, len(data))
	}
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender), "read after reopen")
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
	testutil.RequireNoErrorf(t, err, "open app")
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
	testutil.RequireNoErrorf(t, app.Close(), "close crashed app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	_, err = reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	prepared, err := reopened.prepare.Recover()
	testutil.RequireNoErrorf(t, err, "recover prepare log")
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
	testutil.RequireNoErrorf(t, err, "open app")
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
	testutil.RequireNoErrorf(t, app.Close(), "close crashed app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	_, err = reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	requireCode(t, err, codes.NotFound)
	prepared, err := reopened.prepare.Recover()
	testutil.RequireNoErrorf(t, err, "recover prepare log")
	if len(prepared) != 1 || prepared[0].Identity != doc {
		t.Fatalf("prepared documents = %#v, want prepared document invisible", prepared)
	}
	readLength := prepared[0].Length
	testutil.RequireNoErrorf(t, reopened.blocks.VerifyRange(prepared[0].Location, 0, &readLength), "verify prepared local bytes")
}

func TestCrashAfterMetadataApplyKeepsCommittedDocumentRetryable(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("metadata committed before ack")
	init := writeInitForCrashBoundary(doc, data, "crash-after-metadata-apply")
	app, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open app")
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
	testutil.RequireNoErrorf(t, app.Close(), "close crashed app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head committed document after reopen")
	if head.Length != uint64(len(data)) {
		t.Fatalf("head length = %d, want %d", head.Length, len(data))
	}
	stored, err := reopened.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head committed document from metadata")
	if _, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID); !errors.Is(err, metastore.ErrNotFound) {
		t.Fatalf("upload intent before replay error = %v, want not found", err)
	}
	replayed, err := reopened.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "idempotent replay after metadata crash")
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
	testutil.RequireNoErrorf(t, err, "open app")
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
	testutil.RequireNoErrorf(t, app.Close(), "close crashed app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head committed document after reopen")
	stored, err := reopened.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head committed document from metadata")
	if _, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID); err != nil {
		t.Fatalf("get upload intent after ack-boundary crash: %v", err)
	}
	replayed, err := reopened.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "idempotent replay after ack-boundary crash")
	if !replayed.IdempotentReplay || replayed.Metadata.Identity != head.Identity {
		t.Fatalf("replay = %#v, want idempotent committed document", replayed)
	}
}

func TestPrepareLogRecoveryTruncatesCrashCutTail(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("valid prepared record")
	app, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open app")
	if _, err := app.WriteDocument(context.Background(), writeInitForCrashBoundary(doc, data, "valid-prepare"), newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	appendPrepareLogTail(t, dir, []byte{0, 0, 0, 128, 'p', 'a', 'r'})
	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app with crash-cut prepare log")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	prepared, err := reopened.prepare.Recover()
	testutil.RequireNoErrorf(t, err, "recover truncated prepare log")
	if len(prepared) != 1 || prepared[0].Identity != doc {
		t.Fatalf("prepared documents = %#v, want valid committed document only", prepared)
	}
	payload, err := metastore.MarshalDocument(stored)
	testutil.RequireNoErrorf(t, err, "marshal stored document")
	info, err := os.Stat(filepath.Join(dir, prepareLogName))
	testutil.RequireNoErrorf(t, err, "stat prepare log")
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
	testutil.RequireNoErrorf(t, err, "open app")
	app.now = fixedClock(time.Unix(100, 0).UTC())
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")
	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head rebuilt document")
	if head.Length != uint64(len(data)) {
		t.Fatalf("rebuilt length = %d, want %d", head.Length, len(data))
	}
	transaction, err := reopened.GetTransaction(context.Background(), api.GetTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
	})
	testutil.RequireNoErrorf(t, err, "get rebuilt transaction")
	if transaction.CompletedAt == nil || !transaction.CompletedAt.Equal(completedAt) {
		t.Fatalf("rebuilt completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	}
	if transaction.Tags["closed_by"] != "projection-rebuild-test" {
		t.Fatalf("rebuilt transaction tags = %#v", transaction.Tags)
	}
	storedDocument, err := reopened.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head rebuilt document for upload intent")
	intent, err := reopened.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get rebuilt upload intent")
	if intent.State != metastore.UploadStatePending {
		t.Fatalf("rebuilt upload intent = %#v, want pending", intent)
	}
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, reopened.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender), "read rebuilt document")
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read rebuilt document = %q, want %q", got, data)
	}
	queue, err := reopened.GetRepairQueue(context.Background(), "local")
	testutil.RequireNoErrorf(t, err, "get repair queue after clean rebuild")
	if len(queue) != 0 {
		t.Fatalf("repair queue after clean rebuild = %#v, want empty", queue)
	}
}

func TestMetadataProjectionRebuildQueuesRepairForMissingLocalRef(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("missing after projection rebuild")
	app, stored := writeDocumentForProjectionRebuild(t, dir, doc, data)
	testutil.RequireNoErrorf(t, app.Close(), "close app")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	head, err := reopened.HeadDocument(context.Background(), api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head rebuilt missing-ref document")
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
	testutil.RequireNoErrorf(t, err, "open block")
	if _, err := file.WriteAt([]byte("X"), testStoredOffset(t, stored.Location.StoredOffset)); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close block")
	testutil.RequireNoErrorf(t, app.Close(), "close app")
	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	assertUnreadableRepairRef(t, reopened, doc, stored.Location.BlockID)
}

func TestOpenVerifiesLocalRefsWhenProjectionExists(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("corrupt without projection rebuild")
	app, stored := writeDocumentForProjectionRebuild(t, dir, doc, data)
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	if _, err := file.WriteAt([]byte("X"), testStoredOffset(t, stored.Location.StoredOffset)); err != nil {
		_ = file.Close()
		t.Fatalf("corrupt block: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close block")
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	assertUnreadableRepairRef(t, reopened, doc, stored.Location.BlockID)
}

func TestByteServingReadinessVerificationIsCancelable(t *testing.T) {
	dir := t.TempDir()
	doc := testDocumentIdentity()
	data := []byte("cancel byte readiness")
	app, _ := writeDocumentForProjectionRebuild(t, dir, doc, data)
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reopened, err := OpenWithContext(ctx, dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()

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
	testutil.RequireNoErrorf(t, err, "open app")
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	storedDocument, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	_, err = app.blocks.SealCurrent(ctx)
	testutil.RequireNoErrorf(t, err, "seal current block")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	result, err := app.BackendUploadProcessor(backendStore).RunOnce(ctx)
	testutil.RequireNoErrorf(t, err, "run backend upload processor")
	requireUploadRunResult(t, result, 1, 1, 0, 0)
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	testutil.RequireEqualf(t, intent.State, metastore.UploadStateUploaded, "upload intent state")
	requireBackendObject(ctx, t, backendStore, intent.BackendObjectKey)
	requireBackendObject(ctx, t, backendStore, intent.IndexObjectKey)
	requireBackendObject(ctx, t, backendStore, intent.EnvelopeObjectKey)
	testutil.RequireNoErrorf(t, app.Close(), "close app")
	testutil.RequireNoErrorf(t, os.RemoveAll(filepath.Join(dir, "metadata")), "remove metadata projection")
	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	intent, err = reopened.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get rebuilt upload intent")
	testutil.RequireEqualf(t, intent.State, metastore.UploadStateUploaded, "rebuilt upload intent state")
}

func TestRunBackendUploadOnceSealsDueBlockAndUploads(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("seal and upload")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	storedDocument, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")

	result, err := app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	testutil.RequireTruef(t, result.Sealed, "backend upload result did not seal")
	testutil.RequireEqualf(t, result.SealedBlockID, storedDocument.Location.BlockID, "sealed block id")
	requireUploadRunResult(t, result.Upload, 1, 1, 0, 0)
	testutil.RequireTruef(t, result.MetadataPublished, "metadata was not published")
	testutil.RequireNotNilf(t, result.MetadataPublication, "metadata publication")
	_, err = published.UnmarshalCurrentPointer(readBackendObject(ctx, t, backendStore, result.MetadataPublication.PointerKey))
	testutil.RequireNoErrorf(t, err, "read published current pointer")
	intent, err := app.metadata.GetUploadIntent(storedDocument.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	testutil.RequireEqualf(t, intent.State, metastore.UploadStateUploaded, "upload intent state")
	requireBackendObject(ctx, t, backendStore, intent.IndexObjectKey)
	requireBackendObject(ctx, t, backendStore, intent.EnvelopeObjectKey)
}

func TestRunBackendUploadOnceDoesNotPublishDeferredOpenBlock(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("deferred open block")
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")

	result, err := app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	if result.Sealed || result.Upload.Scanned != 1 || result.Upload.Deferred != 1 || result.Upload.Uploaded != 0 {
		t.Fatalf("upload result = %#v, want one deferred open block", result)
	}
	if result.MetadataPublished || result.MetadataPublication != nil {
		t.Fatalf("metadata publication = %#v, want no checkpoint until a block uploads", result.MetadataPublication)
	}
	if _, err := published.VerifyCurrentCheckpoint(ctx, backendStore, localPublishedCellID); !errors.Is(err, published.ErrCurrentPointerNotFound) {
		t.Fatalf("current checkpoint error = %v, want missing pointer", err)
	}
}

func TestRunBackendUploadOnceSealsRecoveredPendingBlockAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open app")
	data := []byte("recovered pending upload block")
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.Close(), "close app")
	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")

	result, err := reopened.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	if result.Sealed || result.Upload.Uploaded != 1 || result.Upload.Deferred != 0 || !result.MetadataPublished {
		t.Fatalf("backend upload result = %#v, want recovered block upload and checkpoint", result)
	}
	if !stringSliceContains(result.SealedBlockIDs, stored.Location.BlockID) {
		t.Fatalf("sealed block ids = %#v, want recovered block %q", result.SealedBlockIDs, stored.Location.BlockID)
	}
	intent, err := reopened.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if intent.State != metastore.UploadStateUploaded {
		t.Fatalf("upload intent = %#v, want uploaded", intent)
	}
}

func TestRunBackendUploadOnceRepublishesMissingCurrentPointerAfterUploadedIntentRestart(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("uploaded intent missing pointer")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	first, err := app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run initial backend upload once")
	if !first.MetadataPublished || first.MetadataPublication == nil {
		t.Fatalf("initial metadata publication = %#v, want published", first.MetadataPublication)
	}
	missingPointer := &missingCurrentPointerBackend{
		MutableStore:      backendStore,
		missingPointerKey: first.MetadataPublication.PointerKey,
	}

	second, err := app.RunBackendUploadOnce(ctx, missingPointer)
	testutil.RequireNoErrorf(t, err, "rerun backend upload once")
	if second.Upload.Uploaded != 0 || second.Upload.Deferred != 0 || !second.MetadataPublished {
		t.Fatalf("rerun result = %#v, want pointer republished without uploading", second)
	}
	if _, err := published.VerifyCurrentCheckpoint(ctx, missingPointer, localPublishedCellID); err != nil {
		t.Fatalf("verify republished current checkpoint: %v", err)
	}
}

func TestReadDocumentFallsBackToVerifiedBackendCopy(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(250, 0).UTC())
	data := []byte("backend fallback bytes")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	if _, err := file.WriteAt([]byte("X"), testStoredOffset(t, stored.Location.StoredOffset)); err != nil {
		t.Fatalf("corrupt block: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close block")

	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read document")
	if sender.metadata.Source != storageapp.StorageSourceBackend {
		t.Fatalf("read source = %v, want backend", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("read bytes = %q, want %q", got, data)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	if len(queue) != 1 ||
		queue[0].GetTarget().GetDocument().GetDocumentName() != doc.DocumentName ||
		queue[0].GetReason() == "" ||
		queue[0].GetDetectedAt().AsTime() != time.Unix(250, 0).UTC() {
		t.Fatalf("repair queue = %#v, want quarantined local ref", queue)
	}
}

func TestVerificationWindowAcceptsFrameAtSegmentOffsetZero(t *testing.T) {
	start, end, err := verificationWindow(blockstore.Record{
		StoredOffset: 0,
		StoredLength: 4,
		Frames: []blockstore.FrameRecord{
			{SegmentOffset: 0, SegmentLength: 4},
		},
	}, 0, 4)
	testutil.RequireNoErrorf(t, err, "verification window")
	testutil.RequireEqualf(t, start, uint64(0), "verification start")
	testutil.RequireEqualf(t, end, uint64(4), "verification end")
}

func TestVerificationWindowKeepsOffsetZeroStartAcrossFrames(t *testing.T) {
	start, end, err := verificationWindow(blockstore.Record{
		StoredOffset:  0,
		StoredLength:  1536,
		LogicalSHA256: sha256.Sum256([]byte("unused")),
		Frames: []blockstore.FrameRecord{
			{SegmentOffset: 0, SegmentLength: 512},
			{SegmentOffset: 512, SegmentLength: 512},
			{SegmentOffset: 1024, SegmentLength: 512},
		},
	}, 0, 1536)
	testutil.RequireNoErrorf(t, err, "verification window")
	testutil.RequireEqualf(t, start, uint64(0), "verification start")
	testutil.RequireEqualf(t, end, uint64(1536), "verification end")
}

func TestVerificationWindowRejectsWindowStartingAfterSelection(t *testing.T) {
	_, _, err := verificationWindow(blockstore.Record{
		StoredOffset: 0,
		StoredLength: 1024,
		Frames: []blockstore.FrameRecord{
			{SegmentOffset: 256, SegmentLength: 512},
		},
	}, 0, 512)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("verification window error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestVerifyFetchedBackendWindowRejectsFrameBeforeWindow(t *testing.T) {
	frameData := []byte("data")
	frameSHA256 := sha256.Sum256(frameData)
	err := verifyFetchedBackendWindow(blockstore.Record{
		StoredOffset:  0,
		StoredLength:  uint64(len(frameData)),
		LogicalSHA256: frameSHA256,
		Frames: []blockstore.FrameRecord{
			{SegmentOffset: 0, SegmentLength: uint64(len(frameData)), SHA256: frameSHA256},
		},
	}, 0, uint64(len(frameData)), 1, frameData)
	if !errors.Is(err, ErrCorruptVerificationWindow) {
		t.Fatalf("verify fetched backend window error = %v, want %v", err, ErrCorruptVerificationWindow)
	}
}

func TestVerifyFetchedBackendWindowRejectsFrameBeyondFetchedData(t *testing.T) {
	frameData := []byte("data")
	frameSHA256 := sha256.Sum256(frameData)
	err := verifyFetchedBackendWindow(blockstore.Record{
		StoredOffset:  0,
		StoredLength:  uint64(len(frameData)),
		LogicalSHA256: frameSHA256,
		Frames: []blockstore.FrameRecord{
			{SegmentOffset: 0, SegmentLength: uint64(len(frameData)), SHA256: frameSHA256},
		},
	}, 0, uint64(len(frameData)), 0, frameData[:len(frameData)-1])
	if !errors.Is(err, ErrCorruptVerificationWindow) {
		t.Fatalf("verify fetched backend window error = %v, want %v", err, ErrCorruptVerificationWindow)
	}
}

func TestVerifyFetchedBackendWindowWithoutFrames(t *testing.T) {
	documentData := []byte("whole")
	documentSHA256 := sha256.Sum256(documentData)
	record := blockstore.Record{
		StoredOffset:  7,
		StoredLength:  uint64(len(documentData)),
		LogicalSHA256: documentSHA256,
	}
	for _, tc := range []struct {
		name        string
		verifyStart uint64
		data        []byte
		want        error
	}{
		{name: "match", verifyStart: 7, data: documentData},
		{name: "start mismatch", verifyStart: 8, data: documentData, want: ErrCorruptVerificationWindow},
		{name: "short data", verifyStart: 7, data: documentData[:len(documentData)-1], want: io.ErrUnexpectedEOF},
		{name: "checksum mismatch", verifyStart: 7, data: []byte("WHole"), want: blockstore.ErrChecksumMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyFetchedBackendWindow(record, 0, uint64(len(documentData)), tc.verifyStart, tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("verify fetched backend window error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyFetchedBackendWindowFrameSelectionAndChecksum(t *testing.T) {
	frameData := []byte("data")
	frameSHA256 := sha256.Sum256(frameData)
	for _, tc := range []struct {
		name   string
		record blockstore.Record
		offset uint64
		length uint64
		data   []byte
		want   error
	}{
		{
			name: "skips frame outside selection",
			record: blockstore.Record{StoredLength: 16, Frames: []blockstore.FrameRecord{
				{SegmentOffset: 8, SegmentLength: uint64(len(frameData)), SHA256: frameSHA256},
			}},
			offset: 0,
			length: 4,
		},
		{
			name: "accepts selected frame",
			record: blockstore.Record{StoredLength: 16, Frames: []blockstore.FrameRecord{
				{SegmentOffset: 8, SegmentLength: uint64(len(frameData)), SHA256: frameSHA256},
			}},
			offset: 8,
			length: uint64(len(frameData)),
			data:   frameData,
		},
		{
			name: "rejects frame checksum mismatch",
			record: blockstore.Record{StoredLength: 16, Frames: []blockstore.FrameRecord{
				{SegmentOffset: 8, SegmentLength: uint64(len(frameData))},
			}},
			offset: 8,
			length: uint64(len(frameData)),
			data:   frameData,
			want:   blockstore.ErrChecksumMismatch,
		},
		{
			name: "rejects frame offset overflow",
			record: blockstore.Record{StoredLength: 16, Frames: []blockstore.FrameRecord{
				{SegmentOffset: ^uint64(0), SegmentLength: 2},
			}},
			offset: 0,
			length: 1,
			want:   blockstore.ErrInvalidRange,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyFetchedBackendWindow(tc.record, tc.offset, tc.length, tc.record.Frames[0].SegmentOffset, tc.data)
			if !errors.Is(err, tc.want) {
				t.Fatalf("verify fetched backend window error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerificationOutcomeClassifiesKnownOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "match", want: observe.VerificationOutcomeMatch},
		{name: "mismatch", err: blockstore.ErrInvalidRange, want: observe.VerificationOutcomeMismatch},
		{name: "window error", err: ErrCorruptVerificationWindow, want: observe.VerificationOutcomeError},
		{name: "skipped", err: context.Canceled, want: observe.VerificationOutcomeSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireEqualf(t, verificationOutcome(tc.err), tc.want, "verification outcome")
		})
	}
}

func TestCorruptVerificationWindowIsIntegrityFailure(t *testing.T) {
	if !isIntegrityFailure(ErrCorruptVerificationWindow) {
		t.Fatalf("isIntegrityFailure(%v) = false, want true", ErrCorruptVerificationWindow)
	}
}

func TestChunkReaderRejectsEmptyChunkWithoutDrainingStream(t *testing.T) {
	emptyChunks := make([][]byte, 1000)
	chunks := newChunkReader(emptyChunks)
	reader := &chunkReader{chunks: chunks}

	n, err := reader.Read(make([]byte, 1))
	testutil.RequireEqualf(t, n, 0, "read byte count")
	if !errors.Is(err, errEmptyWriteChunk) {
		t.Fatalf("read error = %v, want %v", err, errEmptyWriteChunk)
	}
	testutil.RequireEqualf(t, chunks.index, 1, "empty chunks consumed before rejection")
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
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	corruptStoredByte(t, app, stored.Location, 0)
	transit.SetUnavailable(true)

	sender := &recordingReadSender{}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	detail := requireCryptoUnavailableDetail(t, err)
	requireCryptoUnavailableBackendDetail(t, detail, doc.DocumentName)
	requireNoReadDataSent(t, sender)

	transit.SetUnavailable(false)
	transit.SetMissingKey("transit/backend", true)
	sender = &recordingReadSender{}
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender)
	requireCode(t, err, codes.Unavailable)
	requireCryptoUnavailableDetail(t, err)
	requireNoReadDataSent(t, sender)

	transit.SetMissingKey("transit/backend", false)
	sender = &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read document with key material restored")
	requireReadSource(t, sender, storageapp.StorageSourceBackend)
	requireReadBytes(t, sender, data)
}

func requireCryptoUnavailableBackendDetail(t *testing.T, detail *scrapv1.CryptoUnavailableDetail, documentName string) {
	t.Helper()
	testutil.RequireEqualf(t, detail.GetIdentity().GetDocumentName(), documentName, "crypto detail document name")
	testutil.RequireEqualf(t, detail.GetKeyScope(), "backend", "crypto detail key scope")
	testutil.RequireTruef(t, detail.GetRetryHint().GetRetryable(), "crypto detail retryable = false")
}

func requireNoReadDataSent(t *testing.T, sender *recordingReadSender) {
	t.Helper()
	testutil.RequireFalsef(t, sender.sentMetadata, "metadata was sent before read error")
	testutil.RequireEqualf(t, len(sender.chunks), 0, "chunk count before read error")
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
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	before, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(ctx, t, backendStore, intent.EnvelopeObjectKey))
	testutil.RequireNoErrorf(t, err, "unmarshal before envelope")
	operation := queuedOperation("rewrap-op-1", "rewrap", []*adminv1.Target{
		{
			Target: &adminv1.Target_Block{
				Block: &adminv1.BlockTarget{ShardId: "local", BlockId: stored.Location.BlockID},
			},
		},
	})
	operation.Metadata = map[string]string{"scrap.destination_key_id": "transit/backend-v2"}
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, finished.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "rewrap operation state")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["envelopes_rewrapped"], "1", "rewrap counter")
	after, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(ctx, t, backendStore, intent.EnvelopeObjectKey))
	testutil.RequireNoErrorf(t, err, "unmarshal after envelope")
	requireRewrappedEnvelope(t, after, before)
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireRewrapAuditEvent(t, events)

	testutil.RequireNoErrorf(t, store.Put(operation), "put duplicate operation")
	result, err = app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run duplicate queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	duplicateFinished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get duplicate operation")
	testutil.RequireEqualf(t, duplicateFinished.GetProgress().GetCounters()["envelopes_rewrapped"], "0", "duplicate rewrap counter")
	testutil.RequireEqualf(t, duplicateFinished.GetProgress().GetCounters()["envelopes_skipped"], "1", "duplicate skipped counter")
	duplicate, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(ctx, t, backendStore, intent.EnvelopeObjectKey))
	testutil.RequireNoErrorf(t, err, "unmarshal duplicate envelope")
	requireEnvelopeUnchanged(t, duplicate, after)
}

func requireRewrappedEnvelope(t *testing.T, after, before *storagev1.EnvelopeRecord) {
	t.Helper()
	testutil.RequireEqualf(t, after.GetKeyId(), "transit/backend-v2", "rewrapped key id")
	testutil.RequireEqualf(t, after.GetKeyVersion(), uint32(2), "rewrapped key version")
	testutil.RequireFalsef(t, bytes.Equal(after.GetWrappedDek(), before.GetWrappedDek()), "wrapped DEK did not change")
}

func requireRewrapAuditEvent(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 1, "rewrap audit event count")
	testutil.RequireEqualf(t, events[0].GetEventType(), "rewrap_completed", "rewrap audit event type")
	testutil.RequireEqualf(t, events[0].GetOperationType(), "rewrap", "rewrap audit operation type")
	testutil.RequireEqualf(t, events[0].GetActorIdentity(), "test", "rewrap audit actor")
}

func requireEnvelopeUnchanged(t *testing.T, got, want *storagev1.EnvelopeRecord) {
	t.Helper()
	testutil.RequireTruef(t, bytes.Equal(got.GetWrappedDek(), want.GetWrappedDek()), "wrapped DEK changed")
	testutil.RequireTruef(t, bytes.Equal(got.GetEnvelopeSha256(), want.GetEnvelopeSha256()), "envelope checksum changed")
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "restore-cold-1", time.Unix(200, 0).UTC()), "mark cold")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	removeLocalSealIfPresent(t, app, stored.Location.BlockID)
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, finished.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "finished operation state")
	testutil.RequireEqualf(t, finished.GetProgress().GetWorkUnitsCompleted(), uint64(1), "finished work units completed")
	restored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head restored document")
	testutil.RequireEqualf(t, restored.RestoreState, metastore.RestoreStateHot, "restore state")
	testutil.RequireEqualf(t, restored.Availability, metastore.AvailabilityHot, "availability")
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read restored document")
	requireReadSource(t, sender, storageapp.StorageSourceLocal)
	requireReadBytes(t, sender, data)
}

func removeLocalSealIfPresent(t *testing.T, app *Application, blockID string) {
	t.Helper()
	err := os.Remove(app.blocks.SealPath(blockID))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	testutil.RequireNoErrorf(t, err, "remove local seal")
}

func TestHeadDocumentReportsColdMetadataWithoutLocalBytes(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("cold metadata is still visible")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "head-cold-1", time.Unix(210, 0).UTC()), "mark cold")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	if err := os.Remove(app.blocks.SealPath(stored.Location.BlockID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove local seal: %v", err)
	}

	metadata, err := app.HeadDocument(ctx, api.HeadDocumentRequest{Identity: doc})
	testutil.RequireNoErrorf(t, err, "head cold document")
	if metadata.Availability != storageapp.DocumentAvailabilityCold ||
		metadata.Length != uint64(len(data)) ||
		metadata.Identity != doc {
		t.Fatalf("metadata = %#v, want cold metadata from metastore", metadata)
	}
}

func TestReadDocumentQueuesRestoreOnColdReadAndRetriesAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := OpenWithContext(ctx, dir)
	testutil.RequireNoErrorf(t, err, "open app")
	store, err := operations.Open(dir)
	testutil.RequireNoErrorf(t, err, "open operation store")
	data := []byte("restore queued by read")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	app.SetOperationStore(store)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "read-cold-1", time.Unix(220, 0).UTC()), "mark cold")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	removeLocalSealIfPresent(t, app, stored.Location.BlockID)

	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	testutil.RequireTruef(t, detail.GetRestoreQueued(), "restore detail = %#v, want queued", detail)
	testutil.RequireEqualf(t, detail.GetRestoreState(), "restore_pending", "restore detail state")
	operationID := restoreOnReadOperationID(stored)
	queued, err := store.Get(operationID)
	testutil.RequireNoErrorf(t, err, "get queued restore-on-read operation")
	requireRestoreOnReadOperation(t, queued)
	testutil.RequireNoErrorf(t, app.Close(), "close app")
	testutil.RequireNoErrorf(t, store.Close(), "close operation store")

	reopened, err := OpenWithContext(ctx, dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	reopened.SetBackendStore(backendStore)
	reopenedStore, err := operations.Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen operation store")
	defer func() { testutil.RequireNoErrorf(t, reopenedStore.Close(), "close reopened store") }()
	reopened.SetOperationStore(reopenedStore)
	result, err := reopened.RunQueuedOperationsOnce(ctx, reopenedStore)
	testutil.RequireNoErrorf(t, err, "run queued restore after restart")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	events, err := reopenedStore.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireRestoreQueuedAndCompletedEvents(t, events)
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, reopened.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read restored document")
	requireReadSource(t, sender, storageapp.StorageSourceLocal)
	requireReadBytes(t, sender, data)
}

func requireRestoreOnReadOperation(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetOperationType(), "restore", "restore-on-read operation type")
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "restore-on-read state")
	testutil.RequireEqualf(t, operation.GetMetadata()[operationLaneMetadata], operationLaneInteractive, "restore-on-read operation lane")
	testutil.RequireEqualf(t, operation.GetMetadata()[backendLaneMetadata], string(backend.LaneRestore), "restore-on-read backend lane")
}

func requireRestoreQueuedAndCompletedEvents(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	eventTypes := make(map[string]bool, len(events))
	for _, event := range events {
		eventTypes[event.GetEventType()] = true
	}
	testutil.RequireEqualf(t, len(events), 2, "restore audit event count")
	testutil.RequireTruef(t, eventTypes[operationEventRestoreQueued], "restore queued event missing")
	testutil.RequireTruef(t, eventTypes[operationEventRestoreComplete], "restore complete event missing")
}

func TestReadDocumentRequeuesTerminalRestoreOnReadOperation(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	app.SetOperationStore(store)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("terminal restore retry")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "terminal-cold-1", time.Unix(225, 0).UTC()), "mark cold")
	operationID := restoreOnReadOperationID(stored)
	terminal := queuedOperation(operationID, "restore", []*adminv1.Target{documentTarget(doc)})
	terminal.State = adminv1.OperationState_OPERATION_STATE_FAILED
	terminal.FinishedAt = timestamppb.New(time.Unix(226, 0).UTC())
	terminal.LastError = &adminv1.OperationError{Code: "SCRAP_RESTORE_FAILED", Message: "previous restore failed"}
	testutil.RequireNoErrorf(t, store.Put(terminal), "put terminal operation")

	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	if !detail.GetRestoreQueued() {
		t.Fatalf("restore detail = %#v, want queued retry", detail)
	}
	requeued, err := store.Get(operationID)
	testutil.RequireNoErrorf(t, err, "get requeued operation")
	if requeued.GetState() != adminv1.OperationState_OPERATION_STATE_QUEUED ||
		requeued.GetFinishedAt() != nil ||
		requeued.GetLastError() != nil ||
		requeued.GetProgress().GetMessage() != "requeued by cold read" {
		t.Fatalf("requeued operation = %#v, want queued clean retry", requeued)
	}
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
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
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "archive-cold-1", time.Unix(230, 0).UTC()), "mark cold")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	removeLocalSealIfPresent(t, app, stored.Location.BlockID)
	archive := &archivePendingBackend{Store: backendStore, pending: true}
	app.SetBackendStore(archive)
	operation := queuedOperation("archive-restore-op-1", "restore", []*adminv1.Target{documentTarget(doc)})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run archive-pending restore")
	requireOperationRunResult(t, result, 1, 0, 1, 0)
	pending, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get pending operation")
	testutil.RequireEqualf(t, pending.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "pending operation state")
	testutil.RequireEqualf(t, pending.GetProgress().GetCounters()["blocks_pending"], "1", "pending block counter")
	cold, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head pending document")
	testutil.RequireEqualf(t, cold.RestoreState, metastore.RestoreStateRestorePending, "restore state")
	testutil.RequireEqualf(t, cold.Availability, metastore.AvailabilityRestorePending, "availability")
	err = app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	requireRestorePendingDetail(t, err)

	archive.pending = false
	result, err = app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "retry archive restore")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
}

func TestRunQueuedOperationsOncePrewarmsDocumentFromBackendAndAudits(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	store := openTestOperationStore(t)
	data := []byte("prewarm me from backend")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCold, "test cold state", "prewarm-cold-1", time.Unix(240, 0).UTC()), "mark cold")
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove local block")
	removeLocalSealIfPresent(t, app, stored.Location.BlockID)
	operation := queuedOperation("prewarm-op-1", "prewarm", []*adminv1.Target{documentTarget(doc)})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued prewarm")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["blocks_restored"], "1", "prewarm restored block count")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["operation_lane"], operationLanePlannedPrewarm, "prewarm operation lane")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["backend_lane"], string(backend.LaneRestore), "prewarm backend lane")
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	testutil.RequireEqualf(t, len(events), 1, "prewarm audit event count")
	testutil.RequireEqualf(t, events[0].GetEventType(), operationEventPrewarmComplete, "prewarm audit event type")
	testutil.RequireEqualf(t, events[0].GetOperationType(), "prewarm", "prewarm audit operation type")
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	if _, err := file.WriteAt([]byte("X"), testStoredOffset(t, stored.Location.StoredOffset)); err != nil {
		t.Fatalf("corrupt block: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close block")
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{}), "read with backend fallback")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one repair success", result)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	if len(queue) != 0 {
		t.Fatalf("repair queue = %#v, want resolved repair", queue)
	}
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read repaired document")
	if sender.metadata.Source != storageapp.StorageSourceLocal {
		t.Fatalf("read source = %v, want repaired local", sender.metadata.Source)
	}
	if got := bytes.Join(sender.chunks, nil); !bytes.Equal(got, data) {
		t.Fatalf("repaired bytes = %q, want %q", got, data)
	}
}

func TestReplaceBlockFromBackendKeepsLocalBlockWhenBackendReadFails(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("failed replace keeps the local block")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	blockPath := app.blocks.BlockPath(stored.Location.BlockID)
	originalBlock := readTestBlockFile(t, blockPath, "read original block")
	app.SetBackendStore(&faultingBackendStore{
		Store:      backendStore,
		readErrors: map[string]error{intent.BackendObjectKey: backend.ErrTransient},
	})

	err = app.replaceBlockFromBackend(ctx, stored.Location.BlockID)
	if !errors.Is(err, backend.ErrTransient) {
		t.Fatalf("replace error = %v, want backend transient", err)
	}
	afterBlock := readTestBlockFile(t, blockPath, "read local block after failed replace")
	if !bytes.Equal(afterBlock, originalBlock) {
		t.Fatalf("local block changed after failed replace")
	}
	if _, err := os.Stat(app.blocks.SealPath(stored.Location.BlockID)); err != nil {
		t.Fatalf("seal marker after failed replace: %v", err)
	}
}

func TestReplaceBlockFromBackendInstallsVerifiedBackendBlock(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	data := []byte("successful replace restores the backend block")
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	backendObject, err := backendStore.HeadObject(ctx, intent.BackendObjectKey)
	testutil.RequireNoErrorf(t, err, "head backend block")
	corruptStoredByte(t, app, stored.Location, 0)

	testutil.RequireNoErrorf(t, app.replaceBlockFromBackend(ctx, stored.Location.BlockID), "replace block from backend")
	blockBytes := readTestBlockFile(t, app.blocks.BlockPath(stored.Location.BlockID), "read replaced block")
	testutil.RequireEqualf(t, sha256.Sum256(blockBytes), backendObject.SHA256, "replaced block checksum")
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read replaced document")
	requireReadSource(t, sender, storageapp.StorageSourceLocal)
	requireReadBytes(t, sender, data)
}

func readTestBlockFile(t *testing.T, path, message string) []byte {
	t.Helper()
	// #nosec G304 -- tests pass blockstore-generated paths rooted in t.TempDir().
	data, err := os.ReadFile(path)
	testutil.RequireNoErrorf(t, err, message)
	return data
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	corruptStoredByte(t, app, stored.Location, 0)
	testutil.RequireNoErrorf(t, app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(270, 0).UTC()), "record local repair state")
	operation := queuedOperation("peer-repair-op-1", "repair", []*adminv1.Target{documentTarget(doc)})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one peer repair success", result)
	}
	if peer.repairReadCount != 1 {
		t.Fatalf("peer repair reads = %d, want 1", peer.repairReadCount)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	if len(queue) != 0 {
		t.Fatalf("repair queue = %#v, want resolved repair", queue)
	}
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, app.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read repaired document")
	if sender.metadata.Source != storageapp.StorageSourceLocal {
		t.Fatalf("read source = %v, want repaired local", sender.metadata.Source)
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	peer.repairBytes = append([]byte(nil), peer.preparedBytes...)
	peer.repairBytes[0] ^= 0xff
	corruptStoredByte(t, app, stored.Location, 0)
	testutil.RequireNoErrorf(t, app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(280, 0).UTC()), "record local repair state")
	operation := queuedOperation("peer-repair-op-2", "repair", []*adminv1.Target{documentTarget(doc)})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed repair", result)
	}
	if peer.repairReadCount != 1 {
		t.Fatalf("peer repair reads = %d, want 1", peer.repairReadCount)
	}
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
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
	testutil.RequireNoErrorf(t, err, "open app")
	store := openTestOperationStore(t)
	data := []byte("retry repair after restart")
	doc := testDocumentIdentity()
	doc.DocumentName = "restart-peer-repair.xml"
	peer := newRecordingPreparePeer("member-1")
	app.peerPreparePolicy = replication.Policy{TargetReplicaCount: 2, QuorumReplicaCount: 2}
	app.peerPrepareTargets = []replication.Target{{MemberID: "member-1", Preparer: peer}}
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	requireNoErrorBeforeClosef(t, app, err, "write document")
	stored, err := app.metadata.HeadDocument(doc)
	requireNoErrorBeforeClosef(t, app, err, "head stored document")
	corruptStoredByte(t, app, stored.Location, 0)
	err = app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(290, 0).UTC())
	requireNoErrorBeforeClosef(t, app, err, "record local repair state")
	first := queuedOperation("restart-peer-repair-op-1", "repair", []*adminv1.Target{documentTarget(doc)})
	err = store.Put(first)
	requireNoErrorBeforeClosef(t, app, err, "put first operation")
	result, err := app.RunQueuedOperationsOnce(ctx, store)
	requireNoErrorBeforeClosef(t, app, err, "run first queued operations")
	requireOperationRunResultBeforeClose(t, app, result, 1, 0, 0, 1)
	peerBytes := append([]byte(nil), peer.preparedBytes...)
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	reopened.SetPeerRepairSource("member-1", PeerRepairSourceFunc(func(ctx context.Context, _ blockstore.ReplicaRef, writer io.Writer) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := writer.Write(peerBytes)
		return err
	}))
	second := queuedOperation("restart-peer-repair-op-2", "repair", []*adminv1.Target{documentTarget(doc)})
	testutil.RequireNoErrorf(t, store.Put(second), "put second operation")

	result, err = reopened.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run second queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	queue, err := reopened.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	testutil.RequireEqualf(t, len(queue), 0, "repair queue length")
	sender := &recordingReadSender{}
	testutil.RequireNoErrorf(t, reopened.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, sender), "read repaired document")
	requireReadBytes(t, sender, data)
}

func requireNoErrorBeforeClosef(t *testing.T, app *Application, err error, format string) {
	t.Helper()
	if err == nil {
		return
	}
	_ = app.Close()
	testutil.RequireNoErrorf(t, err, format)
}

func requireOperationRunResultBeforeClose(t *testing.T, app *Application, result OperationRunResult, scanned, succeeded, pending, failed int) {
	t.Helper()
	defer func() {
		if t.Failed() {
			_ = app.Close()
		}
	}()
	requireOperationRunResult(t, result, scanned, succeeded, pending, failed)
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
	writeBackendDedupeDocument(ctx, t, app, first, firstData, "first")
	writeBackendDedupeDocument(ctx, t, app, second, secondData, "second")
	firstStored, err := app.metadata.HeadDocument(first)
	testutil.RequireNoErrorf(t, err, "head first document")
	secondStored, err := app.metadata.HeadDocument(second)
	testutil.RequireNoErrorf(t, err, "head second document")
	testutil.RequireEqualf(t, firstStored.Location.BlockID, secondStored.Location.BlockID, "backend dedupe block id")
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.sealBlockAtBytes = app.blocks.CurrentBlockLength()
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	intent, err := app.metadata.GetUploadIntent(firstStored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	countingBackend := &readCountingBackendStore{Store: backendStore}
	app.SetBackendStore(countingBackend)
	corruptStoredByte(t, app, firstStored.Location, 0)
	recordBackendDedupeRepairStates(ctx, t, app, firstStored, secondStored)
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireBackendDedupeRepairResult(t, result, countingBackend, intent.BackendObjectKey)
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	testutil.RequireEqualf(t, len(queue), 0, "repair queue length")
}

func writeBackendDedupeDocument(ctx context.Context, t *testing.T, app *Application, doc identity.Document, data []byte, label string) {
	t.Helper()
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write %s document", label)
}

func recordBackendDedupeRepairStates(ctx context.Context, t *testing.T, app *Application, documents ...metastore.Document) {
	t.Helper()
	for _, stored := range documents {
		err := app.recordDocumentRepairState(ctx, stored, integrityEvidenceID(stored), true, time.Unix(300, 0).UTC())
		testutil.RequireNoErrorf(t, err, "record local repair state for %s", stored.Identity.DocumentName)
	}
}

func requireBackendDedupeRepairResult(
	t *testing.T,
	result OperationRunResult,
	countingBackend *readCountingBackendStore,
	backendObjectKey string,
) {
	t.Helper()
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	testutil.RequireEqualf(t, countingBackend.readObjectRangeCount(backendObjectKey), 1, "block backend read count")
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
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
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	file, err := os.OpenFile(app.blocks.BlockPath(stored.Location.BlockID), os.O_WRONLY, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	_, err = file.WriteAt([]byte("X"), testStoredOffset(t, stored.Location.StoredOffset))
	testutil.RequireNoErrorf(t, err, "corrupt block")
	testutil.RequireNoErrorf(t, file.Close(), "close block")
	operation := queuedOperation("scrub-op-1", "scrub", []*adminv1.Target{
		{
			Target: &adminv1.Target_Shard{
				Shard: &adminv1.ShardTarget{ShardId: "local"},
			},
		},
	})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	testutil.RequireEqualf(t, finished.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "scrub operation state")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["documents_scanned"], "1", "scrub documents scanned")
	testutil.RequireEqualf(t, finished.GetProgress().GetCounters()["repair_queued"], "1", "scrub repair queued")
	queue, err := app.GetRepairQueue(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get repair queue")
	testutil.RequireEqualf(t, len(queue), 1, "repair queue length")
	testutil.RequireEqualf(t, queue[0].GetTarget().GetDocument().GetDocumentName(), doc.DocumentName, "repair queue document")
	testutil.RequireEqualf(t, queue[0].GetDetectedAt().AsTime(), time.Unix(260, 0).UTC(), "repair queue detected_at")
}

func TestReadDocumentReturnsRestorePendingDetail(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("cold bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateRestorePending, "restore requested", "restore-state-1", time.Unix(200, 0).UTC()), "update restore state")

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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("encrypted bytes")})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	testutil.RequireNoErrorf(t, app.authority.UpdateDocumentRestoreState(ctx, doc, metastore.RestoreStateCryptoUnavailable, "key unavailable", "crypto-state-1", time.Unix(201, 0).UTC()), "update restore state")

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
	testutil.RequireNoErrorf(t, app.ReadDocument(context.Background(), api.ReadDocumentRequest{Identity: doc}, sender), "read document")
	if !sender.sentMetadata {
		t.Fatal("read did not send metadata")
	}
	if sender.metadata.Source != storageapp.StorageSourceLocal {
		t.Fatalf("read source = %v, want local", sender.metadata.Source)
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
	testutil.RequireNoErrorf(t, err, "read ranged document")
	if sender.metadata.Source != storageapp.StorageSourceLocal {
		t.Fatalf("read source = %v, want local", sender.metadata.Source)
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
	testutil.RequireNoErrorf(t, os.Remove(app.blocks.BlockPath(stored.Location.BlockID)), "remove block")

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
		DocumentClass:        storageapp.DocumentClassPermanent,
		PriorityClass:        storageapp.PriorityClassNormal,
		ExpectedLength:       &length,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: "replay-key",
		CreatedByService:     "billing-etl",
	}

	first, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "first write")
	appliedIndex := app.authority.AppliedIndex()
	second, err := app.WriteDocument(context.Background(), init, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "replay write")
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
	testutil.RequireNoErrorf(t, err, "get transaction")
	if transaction.DocumentCount != 1 {
		t.Fatalf("document count = %d, want replay to count once", transaction.DocumentCount)
	}
}

func TestDuplicateWriteWithoutMatchingIdempotencyKeyIsConflict(t *testing.T) {
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	init := api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want one success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetFinishedAt() == nil ||
		finished.GetProgress().GetWorkUnitsCompleted() != 1 {
		t.Fatalf("finished operation = %#v, want succeeded", finished)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head document")
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
	_, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data}))
	testutil.RequireNoErrorf(t, err, "write document")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head document")

	adminDoc, err := app.GetAdminDocument(ctx, doc)
	testutil.RequireNoErrorf(t, err, "get admin document")
	requireAdminDocument(t, adminDoc, doc, stored, data)

	adminBlock, err := app.GetAdminBlock(ctx, api.BlockTarget{ShardID: "local", BlockID: stored.Location.BlockID})
	testutil.RequireNoErrorf(t, err, "get admin block")
	requireAdminBlock(t, adminBlock, stored.Location.BlockID, data)

	shard, err := app.GetAdminShard(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get admin shard")
	requireAdminShard(t, shard)

	member, err := app.GetAdminMember(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get admin member")
	requireAdminMember(t, member, time.Unix(200, 0).UTC())

	summary, err := app.GetAdminClusterSummary(ctx)
	testutil.RequireNoErrorf(t, err, "get admin cluster summary")
	requireAdminClusterSummary(t, summary)

	runway, err := app.GetAdminCapacityRunway(ctx, "")
	testutil.RequireNoErrorf(t, err, "get admin capacity runway")
	requireAdminCapacityRunway(t, runway)
}

func requireAdminDocument(t *testing.T, adminDoc *adminv1.AdminDocument, doc identity.Document, stored metastore.Document, data []byte) {
	t.Helper()
	testutil.RequireEqualf(t, adminDoc.GetShardId(), "local", "admin document shard")
	testutil.RequireEqualf(t, adminDoc.GetLength(), uint64(len(data)), "admin document length")
	testutil.RequireTruef(t, bytes.Equal(adminDoc.GetLogicalSha256(), stored.LogicalSHA256[:]), "admin document checksum")
	testutil.RequireEqualf(t, adminDoc.GetDocument().GetTenantId(), doc.TenantID, "admin document tenant")
	testutil.RequireEqualf(t, adminDoc.GetDocument().GetTransactionId(), doc.TransactionID, "admin document transaction")
	testutil.RequireEqualf(t, adminDoc.GetDocument().GetDocumentName(), doc.DocumentName, "admin document name")
	testutil.RequireEqualf(t, len(adminDoc.GetBlockIds()), 1, "admin document block count")
	testutil.RequireEqualf(t, adminDoc.GetBlockIds()[0], stored.Location.BlockID, "admin document block id")
	testutil.RequireFalsef(t, adminDoc.GetRepairRequired(), "admin document repair required")
}

func requireAdminBlock(t *testing.T, block *adminv1.Block, blockID string, data []byte) {
	t.Helper()
	testutil.RequireEqualf(t, block.GetShardId(), "local", "admin block shard")
	testutil.RequireEqualf(t, block.GetBlockId(), blockID, "admin block id")
	testutil.RequireEqualf(t, block.GetLength(), blockstore.HeaderLength+uint64(len(data)), "admin block length")
	testutil.RequireEqualf(t, len(block.GetChecksum()), sha256.Size, "admin block checksum length")
	testutil.RequireEqualf(t, len(block.GetReplicaMemberIds()), 1, "admin block replica count")
	testutil.RequireEqualf(t, block.GetReplicaMemberIds()[0], "local", "admin block replica")
	testutil.RequireEqualf(t, block.GetBackendObjectKey(), "blocks/"+blockID+".blk", "admin block backend key")
}

func requireAdminShard(t *testing.T, shard *adminv1.Shard) {
	t.Helper()
	testutil.RequireEqualf(t, shard.GetShardId(), "local", "admin shard id")
	testutil.RequireEqualf(t, shard.GetLeaderMemberId(), "local", "admin shard leader")
	testutil.RequireEqualf(t, len(shard.GetVoterMemberIds()), 1, "admin shard voter count")
	testutil.RequireEqualf(t, shard.GetVoterMemberIds()[0], "local", "admin shard voter")
	testutil.RequireTruef(t, shard.GetCommittedIndex() != 0, "admin shard committed index is zero")
	testutil.RequireEqualf(t, shard.GetAppliedIndex(), shard.GetCommittedIndex(), "admin shard applied index")
}

func requireAdminMember(t *testing.T, member *adminv1.StorageMember, lastSeen time.Time) {
	t.Helper()
	testutil.RequireEqualf(t, member.GetStorageMemberId(), "local", "member id")
	testutil.RequireEqualf(t, member.GetCellId(), "local", "member cell")
	testutil.RequireEqualf(t, member.GetState(), adminv1.MemberState_MEMBER_STATE_ONLINE, "member state")
	testutil.RequireTruef(t, member.GetBytesUsed() != 0, "member bytes used is zero")
	testutil.RequireTruef(t, member.GetBytesCapacity() != 0, "member bytes capacity is zero")
	testutil.RequireEqualf(t, member.GetLastSeenAt().AsTime(), lastSeen, "member last seen")
}

func requireAdminClusterSummary(t *testing.T, summary *adminv1.ClusterSummary) {
	t.Helper()
	testutil.RequireEqualf(t, summary.GetShardCount(), uint32(1), "summary shard count")
	testutil.RequireEqualf(t, summary.GetStorageMemberCount(), uint32(1), "summary member count")
	testutil.RequireTruef(t, summary.GetLocalBytesUsed() != 0, "summary bytes used is zero")
	testutil.RequireTruef(t, summary.GetLocalBytesCapacity() != 0, "summary bytes capacity is zero")
}

func requireAdminCapacityRunway(t *testing.T, runway *adminv1.CapacityRunway) {
	t.Helper()
	testutil.RequireEqualf(t, runway.GetCapacityProfileId(), "local-non-production", "capacity profile id")
	testutil.RequireTruef(t, runway.GetUsableBytesRemaining() != 0, "capacity usable bytes remaining is zero")
	testutil.RequireEqualf(t, runway.GetEstimatedBytesPerDay(), uint64(0), "capacity estimated bytes per day")
	testutil.RequireEqualf(t, runway.GetRunwayDays(), uint32(0), "capacity runway days")
	testutil.RequireTruef(t, len(runway.GetWarnings()) != 0, "capacity warnings missing")
}

func TestLocalMemberCordonStatePersists(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	app, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open app")
	member, err := app.CordonMember(ctx, api.MemberMutationRequest{
		OperationID:   "cordon-1",
		StorageMember: "local",
		Reason:        "maintenance",
	})
	testutil.RequireNoErrorf(t, err, "cordon member")
	if !member.GetCordoned() {
		t.Fatalf("member = %#v, want cordoned", member)
	}
	testutil.RequireNoErrorf(t, app.Close(), "close app")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen app")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	member, err = reopened.GetAdminMember(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get reopened member")
	if !member.GetCordoned() {
		t.Fatalf("reopened member = %#v, want persisted cordon", member)
	}
	safety, err := reopened.GetEvictionSafety(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get eviction safety")
	if safety.GetSafeToEvict() || len(safety.GetWarnings()) == 0 {
		t.Fatalf("eviction safety = %#v, want unsafe single-member warning", safety)
	}
	member, err = reopened.UncordonMember(ctx, api.MemberMutationRequest{
		OperationID:   "uncordon-1",
		StorageMember: "local",
	})
	testutil.RequireNoErrorf(t, err, "uncordon member")
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
		DocumentClass:        storageapp.DocumentClassPermanent,
		PriorityClass:        storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, err, "replay existing document while cordoned")
	if !replayed.IdempotentReplay {
		t.Fatalf("replay = %#v, want idempotent replay", replayed)
	}
}

func TestLocalRecoveryReadinessFailsClosedWithoutPublishedMetadata(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	readiness, err := app.GetRecoveryReadiness(ctx)
	testutil.RequireNoErrorf(t, err, "get recovery readiness")
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
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("published metadata bytes")}))
	testutil.RequireNoErrorf(t, err, "write document")
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	stored, err := app.metadata.HeadDocument(testDocumentIdentity())
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")

	publication, err := app.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")
	requireSnapshotPublication(t, publication)
	pointer, err := published.UnmarshalCurrentPointer(readBackendObject(ctx, t, backendStore, publication.PointerKey))
	testutil.RequireNoErrorf(t, err, "unmarshal current pointer")
	testutil.RequireEqualf(t, pointer.GetManifestId(), publication.Manifest.GetManifestId(), "published pointer manifest id")
	testutil.RequireEqualf(t, pointer.GetPublishedAt().AsTime(), app.now(), "published pointer timestamp")
	required := publication.Manifest.GetRequiredObjects()
	requirePublishedRequiredObjects(t, required, intent)

	readiness, err := app.GetRecoveryReadiness(ctx)
	testutil.RequireNoErrorf(t, err, "get recovery readiness")
	requireRecoveryReadinessReady(t, readiness, app.now())
}

func TestPublishMetadataSnapshotRejectsTypedNilBackendStore(t *testing.T) {
	app := openTestApplication(t)
	var store *backendfs.Store
	app.SetBackendStore(store)

	_, err := app.PublishMetadataSnapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "backend store is not configured") {
		t.Fatalf("PublishMetadataSnapshot error = %v, want not configured", err)
	}
}

func requireSnapshotPublication(t *testing.T, publication published.SnapshotPublication) {
	t.Helper()
	testutil.RequireEqualf(t, publication.DocumentCount, 1, "publication document count")
	testutil.RequireTruef(t, publication.PointerKey != "", "publication pointer key is empty")
	testutil.RequireTruef(t, publication.ManifestKey != "", "publication manifest key is empty")
	testutil.RequireTruef(t, publication.SnapshotKey != "", "publication snapshot key is empty")
}

func requirePublishedRequiredObjects(t *testing.T, required []*publishedv1.ObjectRef, intent metastore.UploadIntent) {
	t.Helper()
	testutil.RequireTruef(t, hasRequiredObject(required, intent.BackendObjectKey), "required block object missing")
	testutil.RequireTruef(t, hasRequiredObject(required, intent.IndexObjectKey), "required index object missing")
	testutil.RequireTruef(t, hasRequiredObject(required, intent.EnvelopeObjectKey), "required envelope object missing")
}

func requireRecoveryReadinessReady(t *testing.T, readiness *adminv1.RecoveryReadiness, expectedCheckpoint time.Time) {
	t.Helper()
	testutil.RequireTruef(t, readiness.GetReady(), "readiness ready = false")
	testutil.RequireEqualf(t, readiness.GetLatestRestorableCheckpointAt().AsTime(), expectedCheckpoint, "latest restorable checkpoint")
	testutil.RequireTruef(t, hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_NON_PRODUCTION_MODE"), "non-production warning missing")
	testutil.RequireTruef(t, hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_MEASURED_EVIDENCE_ONLY"), "measured evidence warning missing")
	testutil.RequireFalsef(t, hasWarningCode(readiness.GetWarnings(), "SCRAP_DR_METADATA_EXPORT_MISSING"), "metadata export missing warning present")
}

func TestRunQueuedOperationsOnceCopyVerifySucceedsWithPublishedCheckpoint(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(600, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("copy verify bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("copy verify bytes")}))
	testutil.RequireNoErrorf(t, err, "write document")
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	publication, err := app.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")

	store := openTestOperationStore(t)
	operation := queuedOperation("copy-verify-op-1", "copy-verify", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	requireVerifiedCheckpointOperation(t, finished, publication.Manifest.GetManifestId())
}

func requireVerifiedCheckpointOperation(t *testing.T, operation *adminv1.Operation, manifestID string) {
	t.Helper()
	counters := operation.GetProgress().GetCounters()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "operation state")
	testutil.RequireEqualf(t, counters["manifest_id"], manifestID, "manifest id counter")
	testutil.RequireTruef(t, counters["verified_objects"] != "", "verified objects counter is empty")
	testutil.RequireEqualf(t, counters["verified_block_objects"], "1", "verified block objects")
	testutil.RequireEqualf(t, counters["verified_index_objects"], "1", "verified index objects")
	testutil.RequireEqualf(t, counters["verified_envelope_objects"], "1", "verified envelope objects")
	testutil.RequireEqualf(t, counters["recovery_report_kind"], recoveryEvidenceReportKind, "recovery report kind")
	testutil.RequireEqualf(t, counters["rto_promise"], recoveryNoFormalPromise, "RTO promise")
	testutil.RequireEqualf(t, counters["rpo_promise"], recoveryNoFormalPromise, "RPO promise")
}

func TestRunQueuedOperationsOnceMetadataRestoreImportsColdDocuments(t *testing.T) {
	ctx := context.Background()
	source := openTestApplication(t)
	source.now = fixedClock(time.Unix(700, 0).UTC())
	source.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("metadata restore bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	source.SetBackendStore(backendStore)
	doc := testDocumentIdentity()
	_, err = source.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("metadata restore bytes")}))
	testutil.RequireNoErrorf(t, err, "write source document")
	completedAt := time.Unix(710, 0).UTC()
	source.now = fixedClock(completedAt)
	_, err = source.CompleteTransaction(ctx, api.CompleteTransactionRequest{
		Transaction: identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID},
		Tags:        map[string]string{"closed_by": "metadata-restore-test"},
	})
	testutil.RequireNoErrorf(t, err, "complete source transaction")
	_, err = source.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	publication, err := source.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")

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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := restoredApp.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	requireMetadataRestoreFinished(t, finished)

	restored, err := restoredApp.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head restored document")
	requireColdUploadedDocument(t, restored)
	intent, err := restoredApp.metadata.GetUploadIntent(restored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get restored upload intent")
	requireUploadedIntentObjects(t, intent)
	transaction, err := restoredApp.metadata.GetTransaction(identity.Transaction{TenantID: doc.TenantID, TransactionID: doc.TransactionID})
	testutil.RequireNoErrorf(t, err, "get restored transaction")
	requireRestoredTransaction(t, transaction, completedAt)
	err = restoredApp.ReadDocument(ctx, api.ReadDocumentRequest{Identity: doc}, &recordingReadSender{})
	requireCode(t, err, codes.Unavailable)
	detail := requireRestorePendingDetail(t, err)
	testutil.RequireEqualf(t, detail.GetRestoreState(), "cold", "restore detail state")
}

func requireMetadataRestoreFinished(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	counters := operation.GetProgress().GetCounters()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "metadata restore state")
	testutil.RequireEqualf(t, counters["documents"], "1", "metadata restore document count")
	testutil.RequireEqualf(t, counters["transactions"], "1", "metadata restore transaction count")
	testutil.RequireEqualf(t, counters["upload_intents"], "1", "metadata restore upload intent count")
}

func requireColdUploadedDocument(t *testing.T, document metastore.Document) {
	t.Helper()
	testutil.RequireEqualf(t, document.Availability, metastore.AvailabilityCold, "document availability")
	testutil.RequireEqualf(t, document.RestoreState, metastore.RestoreStateCold, "document restore state")
	testutil.RequireEqualf(t, document.UploadState, metastore.UploadStateUploaded, "document upload state")
}

func requireUploadedIntentObjects(t *testing.T, intent metastore.UploadIntent) {
	t.Helper()
	testutil.RequireEqualf(t, intent.State, metastore.UploadStateUploaded, "upload intent state")
	testutil.RequireTruef(t, intent.BackendObjectKey != "", "backend object key is empty")
	testutil.RequireTruef(t, intent.IndexObjectKey != "", "index object key is empty")
	testutil.RequireTruef(t, intent.EnvelopeObjectKey != "", "envelope object key is empty")
}

func requireRestoredTransaction(t *testing.T, transaction metastore.Transaction, completedAt time.Time) {
	t.Helper()
	testutil.RequireEqualf(t, transaction.State, metastore.TransactionStateCompleted, "transaction state")
	testutil.RequireNotNilf(t, transaction.CompletedAt, "transaction completed_at")
	testutil.RequireTruef(t, transaction.CompletedAt.Equal(completedAt), "completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	testutil.RequireEqualf(t, transaction.Tags["closed_by"], "metadata-restore-test", "transaction closed_by tag")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR operation", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("dry-run drill bytes")}))
	testutil.RequireNoErrorf(t, err, "write document")
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	publication, err := app.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")
	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-dry-run-1", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	operation.DryRun = true
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	requireDryRunDRFinished(t, finished)
}

func requireDryRunDRFinished(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	counters := operation.GetProgress().GetCounters()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "dry-run DR state")
	testutil.RequireEqualf(t, counters["documents"], "1", "dry-run DR document count")
	testutil.RequireEqualf(t, counters["upload_intents"], "1", "dry-run DR upload intent count")
	testutil.RequireEqualf(t, counters["blocks_restored"], "0", "dry-run DR restored blocks")
	testutil.RequireEqualf(t, counters["recovery_report_kind"], recoveryEvidenceReportKind, "dry-run DR report kind")
}

func TestRunQueuedOperationsOnceDRDrillRestoresScratchMetadata(t *testing.T) {
	ctx := context.Background()
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-process-tmp"))
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(801, 0).UTC())
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len("scratch drill bytes"))
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	app.SetBackendStore(backendStore)
	_, err = app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         testDocumentIdentity(),
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{[]byte("scratch drill bytes")}))
	testutil.RequireNoErrorf(t, err, "write document")
	_, err = app.RunBackendUploadOnce(ctx, backendStore)
	testutil.RequireNoErrorf(t, err, "run backend upload once")
	publication, err := app.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")
	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-op-2", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	requireDRDrillFinished(t, finished)
}

func requireDRDrillFinished(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	counters := operation.GetProgress().GetCounters()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "DR drill state")
	testutil.RequireEqualf(t, counters["documents"], "1", "DR drill document count")
	testutil.RequireEqualf(t, counters["upload_intents"], "1", "DR drill upload intent count")
	testutil.RequireEqualf(t, counters["blocks_restored"], "1", "DR drill restored blocks")
	testutil.RequireEqualf(t, counters["verified_block_objects"], "1", "DR drill verified block objects")
	testutil.RequireEqualf(t, counters["verified_index_objects"], "1", "DR drill verified index objects")
	testutil.RequireEqualf(t, counters["verified_envelope_objects"], "1", "DR drill verified envelope objects")
	testutil.RequireEqualf(t, counters["recovery_report_kind"], recoveryEvidenceReportKind, "DR drill report kind")
	testutil.RequireEqualf(t, counters["rto_promise"], recoveryNoFormalPromise, "DR drill RTO promise")
	testutil.RequireEqualf(t, counters["rpo_promise"], recoveryNoFormalPromise, "DR drill RPO promise")
}

func TestRunQueuedOperationsOnceDRDrillFailsWhenRequiredArtifactMissing(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(802, 0).UTC())
	backendStore, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	publication, intent := publishDRDrillTestDocument(ctx, t, app, backendStore, []byte("missing artifact drill bytes"))
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, err, "open backend store")
	publication, intent := publishDRDrillTestDocument(ctx, t, app, backendStore, []byte("corrupt artifact drill bytes"))
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, err, "open backend store")
	publication, _ := publishDRDrillTestDocument(ctx, t, app, backendStore, []byte("missing key material drill bytes"))
	transit.SetMissingKey("transit/backend", true)

	store := openTestOperationStore(t)
	operation := queuedOperation("dr-drill-missing-key-1", "dr-drill", []*adminv1.Target{
		{
			Target: &adminv1.Target_Snapshot{
				Snapshot: &adminv1.SnapshotTarget{ShardId: ptr("local"), SnapshotId: publication.Manifest.GetManifestId()},
			},
		},
	})
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed DR drill", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 0 || result.Failed != 1 {
		t.Fatalf("operation result = %#v, want one failed drain", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_FAILED ||
		finished.GetLastError().GetCode() != "SCRAP_DRAIN_UNSAFE" ||
		len(finished.GetWarnings()) == 0 {
		t.Fatalf("finished operation = %#v, want unsafe drain failure with warnings", finished)
	}
	member, err := app.GetAdminMember(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get member")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	if result.Scanned != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("operation result = %#v, want dry-run success", result)
	}
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	if finished.GetState() != adminv1.OperationState_OPERATION_STATE_SUCCEEDED ||
		finished.GetProgress().GetWorkUnitsCompleted() != 1 ||
		len(finished.GetWarnings()) == 0 {
		t.Fatalf("finished operation = %#v, want dry-run success with safety warning", finished)
	}
	member, err := app.GetAdminMember(ctx, "local")
	testutil.RequireNoErrorf(t, err, "get member")
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := store.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get operation")
	requireCapacityOverrideFinished(t, finished)
	events, err := store.ListAuditEvents()
	testutil.RequireNoErrorf(t, err, "list audit events")
	requireCapacityOverrideAudit(t, events)
}

func requireCapacityOverrideFinished(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_SUCCEEDED, "capacity operation state")
	testutil.RequireNilf(t, operation.GetLastError(), "capacity operation last error")
	testutil.RequireEqualf(t, operation.GetProgress().GetCounters()["capacity_profile_id"], "production-a", "capacity profile counter")
	testutil.RequireEqualf(t, operation.GetProgress().GetCounters()["reason"], "incident INC-42", "capacity reason counter")
	testutil.RequireTruef(t, hasWarningCode(operation.GetWarnings(), "SCRAP_CAPACITY_OVERRIDE_RECORDED_ONLY"), "capacity warning missing")
}

func requireCapacityOverrideAudit(t *testing.T, events []*adminv1.AuditEvent) {
	t.Helper()
	testutil.RequireEqualf(t, len(events), 1, "capacity audit event count")
	testutil.RequireEqualf(t, events[0].GetEventType(), "capacity_override_completed", "capacity audit event type")
	testutil.RequireEqualf(t, events[0].GetOperationType(), "capacity-override", "capacity audit operation type")
	testutil.RequireEqualf(t, events[0].GetMetadata()["scrap.capacity_override_reason"], "incident INC-42", "capacity audit reason")
}

func TestRecoverInterruptedOperationsRequeuesRunningCapacityOverrideAfterRestart(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	app.now = fixedClock(time.Unix(500, 0).UTC())
	storeDir := t.TempDir()
	store, err := operations.Open(storeDir)
	testutil.RequireNoErrorf(t, err, "open operation store")
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
	err = store.Put(operation)
	requireNoStoreErrorBeforeClosef(t, store, err, "put operation")
	testutil.RequireNoErrorf(t, store.Close(), "close operation store")

	reopenedStore, err := operations.Open(storeDir)
	testutil.RequireNoErrorf(t, err, "reopen operation store")
	defer func() { testutil.RequireNoErrorf(t, reopenedStore.Close(), "close reopened store") }()
	recovery, err := app.RecoverInterruptedOperations(ctx, reopenedStore)
	testutil.RequireNoErrorf(t, err, "recover interrupted operations")
	testutil.RequireEqualf(t, recovery.Scanned, 1, "recovery scanned count")
	testutil.RequireEqualf(t, recovery.Requeued, 1, "recovery requeued count")
	testutil.RequireEqualf(t, recovery.FailedUnsupported, 0, "recovery failed unsupported count")
	recovered, err := reopenedStore.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get recovered operation")
	requireRecoveredCapacityOverride(t, recovered)

	result, err := app.RunQueuedOperationsOnce(ctx, reopenedStore)
	testutil.RequireNoErrorf(t, err, "run queued operations")
	requireOperationRunResult(t, result, 1, 1, 0, 0)
	finished, err := reopenedStore.Get(operation.GetOperationId())
	testutil.RequireNoErrorf(t, err, "get finished operation")
	requireCapacityOverrideFinished(t, finished)
}

func requireNoStoreErrorBeforeClosef(t *testing.T, store *operations.Store, err error, format string) {
	t.Helper()
	if err == nil {
		return
	}
	_ = store.Close()
	testutil.RequireNoErrorf(t, err, format)
}

func requireRecoveredCapacityOverride(t *testing.T, operation *adminv1.Operation) {
	t.Helper()
	testutil.RequireEqualf(t, operation.GetState(), adminv1.OperationState_OPERATION_STATE_QUEUED, "recovered operation state")
	testutil.RequireEqualf(t, operation.GetProgress().GetCounters()["retry_attempt"], "2", "recovered retry counter")
	testutil.RequireEqualf(t, operation.GetLastError().GetCode(), "SCRAP_CAPACITY_OVERRIDE_RETRYABLE", "recovered last error")
	testutil.RequireEqualf(t, operation.GetMetadata()["audit_correlation_id"], "audit-1", "recovered audit correlation id")
	testutil.RequireTruef(t, hasWarningCode(operation.GetWarnings(), "SCRAP_CAPACITY_OVERRIDE_RETRY"), "capacity retry warning missing")
	testutil.RequireTruef(t, hasWarningCode(operation.GetWarnings(), "SCRAP_OPERATION_RESTART_REQUEUED"), "restart requeued warning missing")
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
			DocumentClass:    storageapp.DocumentClassPermanent,
			PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, store.Put(operation), "put operation")

	result, err := app.RunQueuedOperationsOnce(ctx, store)
	testutil.RequireNoErrorf(t, err, "run queued operations")
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
	testutil.RequireNoErrorf(t, err, "open app")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, app.Close(), "close app")
	})
	return app
}

func openTestOperationStore(t *testing.T) *operations.Store {
	t.Helper()
	store, err := operations.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open operation store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close operation store")
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

type missingCurrentPointerBackend struct {
	backend.MutableStore
	missingPointerKey string
}

func (s *missingCurrentPointerBackend) HeadObject(ctx context.Context, key string) (backend.Object, error) {
	if key == s.missingPointerKey {
		return backend.Object{}, backend.ErrNotFound
	}
	return s.MutableStore.HeadObject(ctx, key)
}

func (s *missingCurrentPointerBackend) ReadObjectRange(ctx context.Context, key string, selected backend.Range, writer io.Writer) error {
	if key == s.missingPointerKey {
		return backend.ErrNotFound
	}
	return s.MutableStore.ReadObjectRange(ctx, key, selected, writer)
}

func (s *missingCurrentPointerBackend) PutMutableObject(ctx context.Context, key string, reader io.Reader) (backend.Object, error) {
	if key == s.missingPointerKey {
		s.missingPointerKey = ""
	}
	return s.MutableStore.PutMutableObject(ctx, key, reader)
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
		DocumentClass:        storageapp.DocumentClassPermanent,
		PriorityClass:        storageapp.PriorityClassNormal,
		ExpectedLength:       &length,
		ExpectedSHA256:       sum[:],
		ClientIdempotencyKey: idempotencyKey,
		CreatedByService:     "billing-etl",
	}
}

func appendPrepareLogTail(t *testing.T, dir string, data []byte) {
	t.Helper()
	// #nosec G304 -- the path is built under the test-owned application directory.
	file, err := os.OpenFile(filepath.Join(dir, prepareLogName), os.O_WRONLY|os.O_APPEND, 0)
	testutil.RequireNoErrorf(t, err, "open prepare log for tail append")
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatalf("append prepare log tail: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatalf("sync prepare log tail: %v", err)
	}
	testutil.RequireNoErrorf(t, file.Close(), "close prepare log tail")
}

func writeLocalReadVerificationDocument(t *testing.T, app *Application, doc identity.Document, data []byte) metastore.Document {
	t.Helper()
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	return stored
}

func corruptStoredByte(t *testing.T, app *Application, record blockstore.Record, offset uint64) {
	t.Helper()
	file, err := os.OpenFile(app.blocks.BlockPath(record.BlockID), os.O_RDWR, 0)
	testutil.RequireNoErrorf(t, err, "open block")
	storedOffset := testStoredOffset(t, record.StoredOffset+offset)
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
	testutil.RequireNoErrorf(t, file.Close(), "close block")
}

func testStoredOffset(t *testing.T, offset uint64) int64 {
	t.Helper()
	got, err := safeconv.Uint64ToInt64("stored offset", offset)
	testutil.RequireNoErrorf(t, err, "convert stored offset")
	return got
}

func writeDocumentForProjectionRebuild(t *testing.T, dir string, doc identity.Document, data []byte) (*Application, metastore.Document) {
	t.Helper()
	app, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open app")
	if _, err := app.WriteDocument(context.Background(), api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
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
	testutil.RequireNoErrorf(t, err, "get repair queue")
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

func (p *recordingPreparePeer) ReadReplica(ctx context.Context, _ blockstore.ReplicaRef, writer io.Writer) error {
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
	testutil.RequireNoErrorf(t, app.authority.Close(), "close authority")
	authority, err := raftmeta.OpenAuthorityWithOptions(filepath.Join(app.dir, "raftmeta"), "local", app.metadata, raftmeta.AuthorityOptions{
		FreshnessChecker: checker,
	})
	testutil.RequireNoErrorf(t, err, "reopen authority with freshness checker")
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
	if got := status.Code(api.ToGRPCError(err)); got != code {
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

func publishDRDrillTestDocument(ctx context.Context, t *testing.T, app *Application, backendStore backend.MutableStore, data []byte) (published.SnapshotPublication, metastore.UploadIntent) {
	t.Helper()
	doc := testDocumentIdentity()
	app.sealBlockAtBytes = blockstore.HeaderLength + uint64(len(data))
	app.SetBackendStore(backendStore)
	if _, err := app.WriteDocument(ctx, api.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "billing-etl",
	}, newChunkReader([][]byte{data})); err != nil {
		t.Fatalf("write document: %v", err)
	}
	if _, err := app.RunBackendUploadOnce(ctx, backendStore); err != nil {
		t.Fatalf("run backend upload once: %v", err)
	}
	publication, err := app.PublishMetadataSnapshot(ctx)
	testutil.RequireNoErrorf(t, err, "publish metadata snapshot")
	stored, err := app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head stored document")
	intent, err := app.metadata.GetUploadIntent(stored.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	return publication, intent
}

func readBackendObject(ctx context.Context, t *testing.T, store backend.Store, key string) []byte {
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

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func requireIntegrityDetail(t *testing.T, err error) *scrapv1.IntegrityFailureDetail {
	t.Helper()
	st, ok := status.FromError(api.ToGRPCError(err))
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
	st, ok := status.FromError(api.ToGRPCError(err))
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
	st, ok := status.FromError(api.ToGRPCError(err))
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
	st, ok := status.FromError(api.ToGRPCError(err))
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
