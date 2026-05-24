package metastore

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPutHeadFindDocument(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)

	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")

	got, err := store.HeadDocument(doc.Identity)
	testutil.RequireNoErrorf(t, err, "head document")
	if got.Identity.DocumentName != doc.Identity.DocumentName {
		t.Fatalf("document name = %q, want %q", got.Identity.DocumentName, doc.Identity.DocumentName)
	}
	if got.LogicalSHA256 != doc.LogicalSHA256 {
		t.Fatal("logical sha was not preserved")
	}
	if got.Location.StoredOffset != doc.Location.StoredOffset {
		t.Fatalf("stored offset = %d, want %d", got.Location.StoredOffset, doc.Location.StoredOffset)
	}
	if got.RestoreState != RestoreStateHot || got.UploadState != UploadStatePending {
		t.Fatalf("restore/upload state = %d/%d, want hot/pending", got.RestoreState, got.UploadState)
	}

	found, err := store.FindDocuments(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, DocumentFilter{
		DocumentNamePrefix:    "invoice",
		HasDocumentNamePrefix: true,
		DocumentClass:         DocumentClassPermanent,
		HasDocumentClass:      true,
		Tags: map[string]string{
			"workflow": "billing",
		},
	})
	testutil.RequireNoErrorf(t, err, "find documents")
	if len(found) != 1 {
		t.Fatalf("found %d documents, want 1", len(found))
	}
}

func TestPutDocumentIsIdempotentAndCountsOnce(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)

	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "idempotent put")

	transaction, err := store.GetTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	})
	testutil.RequireNoErrorf(t, err, "get transaction")
	if transaction.DocumentCount != 1 || transaction.PermanentDocumentCount != 1 {
		t.Fatalf("counts = %#v, want one permanent document", transaction)
	}
}

func TestPutDocumentRejectsConflictingReplay(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")

	conflict := doc
	conflict.Length++
	err := store.PutDocument(conflict)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestCompleteTransactionPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "open store")
	doc := sampleDocument("invoice.xml", DocumentClassEphemeral)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	completedAt := doc.CreatedAt.Add(time.Minute)
	if _, err := store.CompleteTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "test"}); err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	testutil.RequireNoErrorf(t, store.Close(), "close store")

	reopened, err := Open(dir)
	testutil.RequireNoErrorf(t, err, "reopen store")
	defer func() { testutil.RequireNoErrorf(t, reopened.Close(), "close reopened") }()
	transaction, err := reopened.GetTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	})
	testutil.RequireNoErrorf(t, err, "get transaction")
	if transaction.State != TransactionStateCompleted {
		t.Fatalf("state = %d, want completed", transaction.State)
	}
	if transaction.EphemeralDocumentCount != 1 {
		t.Fatalf("ephemeral count = %d, want 1", transaction.EphemeralDocumentCount)
	}
	if transaction.CompletedAt == nil || !transaction.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	}
}

func TestCompleteTransactionIsIdempotentAndPreservesFirstState(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassEphemeral)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	completedAt := doc.CreatedAt.Add(time.Minute)
	first, err := store.CompleteTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "first"})
	testutil.RequireNoErrorf(t, err, "complete transaction")

	retryAt := completedAt.Add(time.Hour)
	retry, err := store.CompleteTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, retryAt, map[string]string{"closed_by": "retry"})
	testutil.RequireNoErrorf(t, err, "retry complete transaction")

	testutil.RequireEqualf(t, retry.State, TransactionStateCompleted, "transaction state")
	testutil.RequireDeepEqualf(t, retry.Tags, first.Tags, "retry tags")
	if retry.CompletedAt == nil || !retry.CompletedAt.Equal(completedAt) {
		t.Fatalf("retry completed_at = %v, want original %v", retry.CompletedAt, completedAt)
	}
}

func TestCompleteTransactionRejectsTimedOutTransaction(t *testing.T) {
	store := openTestStore(t)
	timeoutAt := time.Unix(300, 0).UTC()
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State:     TransactionStateTimedOut,
		CreatedAt: time.Unix(100, 0).UTC(),
		TimeoutAt: &timeoutAt,
		Tags: map[string]string{
			"reason": "timeout",
		},
	}
	installTransactionSnapshot(t, store, transaction)

	_, err := store.CompleteTransaction(transaction.Identity, timeoutAt.Add(time.Minute), map[string]string{"closed_by": "retry"})
	if !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("complete timed-out transaction error = %v, want %v", err, ErrTransactionClosed)
	}
	got, err := store.GetTransaction(transaction.Identity)
	testutil.RequireNoErrorf(t, err, "get transaction")
	testutil.RequireEqualf(t, got.State, TransactionStateTimedOut, "transaction state")
	testutil.RequireNilf(t, got.CompletedAt, "completed_at")
	testutil.RequireDeepEqualf(t, got.Tags, transaction.Tags, "transaction tags")
}

func TestPutDocumentRejectsNewDocumentAfterTransactionCompleted(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "idempotent replay before complete")
	_, err := store.CompleteTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, doc.CreatedAt.Add(time.Minute), nil)
	testutil.RequireNoErrorf(t, err, "complete transaction")
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "idempotent replay after complete")

	late := sampleDocument("late.xml", DocumentClassEphemeral)
	err = store.PutDocument(late)
	if !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("late put error = %v, want %v", err, ErrTransactionClosed)
	}
	if _, err := store.HeadDocument(late.Identity); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late document lookup error = %v, want %v", err, ErrNotFound)
	}
}

func TestListTransactionsReturnsCommittedTransactions(t *testing.T) {
	store := openTestStore(t)
	first := sampleDocument("a.xml", DocumentClassPermanent)
	second := sampleDocument("b.xml", DocumentClassEphemeral)
	second.Identity.TransactionID = "txn-b"
	testutil.RequireNoErrorf(t, store.PutDocument(second), "put second document")
	testutil.RequireNoErrorf(t, store.PutDocument(first), "put first document")

	transactions, err := store.ListTransactions()
	testutil.RequireNoErrorf(t, err, "list transactions")
	if len(transactions) != 2 ||
		transactions[0].Identity.TransactionID != "txn" ||
		transactions[1].Identity.TransactionID != "txn-b" {
		t.Fatalf("transactions = %#v, want ordered transaction records", transactions)
	}
}

func TestRecordUploadIntentPersistsAndLists(t *testing.T) {
	store := openTestStore(t)
	first := sampleUploadIntent("block-1")
	second := sampleUploadIntent("block-2")
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(first), "record first upload intent")
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(second), "record second upload intent")

	got, err := store.GetUploadIntent(first.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if got != first {
		t.Fatalf("upload intent = %#v, want %#v", got, first)
	}
	intents, err := store.ListUploadIntents()
	testutil.RequireNoErrorf(t, err, "list upload intents")
	if len(intents) != 2 || intents[0].BlockID != "block-1" || intents[1].BlockID != "block-2" {
		t.Fatalf("upload intents = %#v, want block-1 then block-2", intents)
	}
}

func TestListPendingUploadIntentsFiltersAndLimits(t *testing.T) {
	store := openTestStore(t)
	uploaded := sampleUploadIntent("block-1")
	uploaded.State = UploadStateUploaded
	uploaded.UpdatedAt = time.Unix(10, 0).UTC()
	pending := sampleUploadIntent("block-2")
	pending.UpdatedAt = time.Unix(40, 0).UTC()
	failed := sampleUploadIntent("block-3")
	failed.State = UploadStateFailed
	failed.UpdatedAt = time.Unix(20, 0).UTC()
	laterPending := sampleUploadIntent("block-4")
	laterPending.UpdatedAt = time.Unix(30, 0).UTC()
	for _, intent := range []UploadIntent{uploaded, pending, failed, laterPending} {
		testutil.RequireNoErrorf(t, store.RecordUploadIntent(intent), "record upload intent %s", intent.BlockID)
	}

	intents, err := store.ListPendingUploadIntents(2)
	testutil.RequireNoErrorf(t, err, "list pending upload intents")
	if len(intents) != 2 || intents[0].BlockID != "block-3" || intents[1].BlockID != "block-4" {
		t.Fatalf("pending intents = %#v, want oldest processable intents", intents)
	}
}

func TestUpdateUploadIntentStateRefreshesPendingIndex(t *testing.T) {
	store := openTestStore(t)
	first := sampleUploadIntent("block-1")
	first.UpdatedAt = time.Unix(10, 0).UTC()
	second := sampleUploadIntent("block-2")
	second.UpdatedAt = time.Unix(20, 0).UTC()
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(first), "record first upload intent")
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(second), "record second upload intent")

	_, err := store.UpdateUploadIntentState(first.BlockID, UploadStateFailed, "retry later", time.Unix(30, 0).UTC())
	testutil.RequireNoErrorf(t, err, "move first upload intent to later retry")
	intents, err := store.ListPendingUploadIntents(2)
	testutil.RequireNoErrorf(t, err, "list pending upload intents")
	if len(intents) != 2 || intents[0].BlockID != second.BlockID || intents[1].BlockID != first.BlockID {
		t.Fatalf("pending intents after retry update = %#v, want second then first", intents)
	}

	_, err = store.UpdateUploadIntentState(second.BlockID, UploadStateUploaded, "", time.Unix(40, 0).UTC())
	testutil.RequireNoErrorf(t, err, "remove uploaded intent from pending index")
	intents, err = store.ListPendingUploadIntents(2)
	testutil.RequireNoErrorf(t, err, "list pending upload intents after upload")
	if len(intents) != 1 || intents[0].BlockID != first.BlockID {
		t.Fatalf("pending intents after upload = %#v, want first only", intents)
	}
}

func TestListBlockDocuments(t *testing.T) {
	store := openTestStore(t)
	first := sampleDocument("invoice.xml", DocumentClassPermanent)
	second := sampleDocument("summary.pdf", DocumentClassPermanent)
	second.Location.StoredOffset = 128
	other := sampleDocument("other.xml", DocumentClassPermanent)
	other.Location.BlockID = "018f6d86-7a22-7abc-8def-222222222222"
	for _, doc := range []Document{second, other, first} {
		if err := store.PutDocument(doc); err != nil {
			t.Fatalf("put document %s: %v", doc.Identity.DocumentName, err)
		}
	}

	documents, err := store.ListBlockDocuments(first.Location.BlockID)
	testutil.RequireNoErrorf(t, err, "list block documents")
	if len(documents) != 2 ||
		documents[0].Identity.DocumentName != "invoice.xml" ||
		documents[1].Identity.DocumentName != "summary.pdf" {
		t.Fatalf("block documents = %#v, want invoice and summary", documents)
	}
}

func TestListDocumentsFiltersAndOrdersByIdentity(t *testing.T) {
	store := openTestStore(t)
	first := sampleDocument("invoice.xml", DocumentClassPermanent)
	second := sampleDocument("summary.pdf", DocumentClassPermanent)
	ephemeral := sampleDocument("scratch.tmp", DocumentClassEphemeral)
	otherTenant := sampleDocument("adjustment.xml", DocumentClassPermanent)
	otherTenant.Identity.TenantID = "tenant-b"
	for _, doc := range []Document{otherTenant, second, ephemeral, first} {
		if err := store.PutDocument(doc); err != nil {
			t.Fatalf("put document %s/%s: %v", doc.Identity.TenantID, doc.Identity.DocumentName, err)
		}
	}

	documents, err := store.ListDocuments(DocumentFilter{
		DocumentClass:    DocumentClassPermanent,
		HasDocumentClass: true,
	})
	testutil.RequireNoErrorf(t, err, "list documents")
	if len(documents) != 3 ||
		documents[0].Identity.DocumentName != "invoice.xml" ||
		documents[1].Identity.DocumentName != "summary.pdf" ||
		documents[2].Identity.TenantID != "tenant-b" ||
		documents[2].Identity.DocumentName != "adjustment.xml" {
		t.Fatalf("documents = %#v, want permanent documents ordered by identity", documents)
	}
}

func TestRecordUploadIntentIsIdempotentAndConflictsOnDifferentKeys(t *testing.T) {
	store := openTestStore(t)
	intent := sampleUploadIntent("block-1")
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(intent), "record upload intent")
	replayed := intent
	replayed.UpdatedAt = replayed.UpdatedAt.Add(time.Minute)
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(replayed), "idempotent upload intent replay")
	got, err := store.GetUploadIntent(intent.BlockID)
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if !got.UpdatedAt.Equal(intent.UpdatedAt) {
		t.Fatalf("updated_at = %v, want original %v", got.UpdatedAt, intent.UpdatedAt)
	}
	conflict := intent
	conflict.BackendObjectKey = "other-object"
	if err := store.RecordUploadIntent(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
	}
}

func TestRecordUploadIntentRejectsInvalidState(t *testing.T) {
	store := openTestStore(t)
	intent := sampleUploadIntent("block-1")
	intent.State = UploadStateNotRequired
	if err := store.RecordUploadIntent(intent); err == nil {
		t.Fatal("invalid upload intent state succeeded")
	}
}

func TestApplyRecordUploadIntentCommand(t *testing.T) {
	store := openTestStore(t)
	proposedAt := time.Unix(30, 0).UTC()
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "upload-1",
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_RecordUploadIntent{
			RecordUploadIntent: &metastorev1.RecordUploadIntentCommand{
				BlockId:           "block-1",
				BackendObjectKey:  "objects/block-1.blk",
				IndexObjectKey:    "objects/block-1.idx",
				EnvelopeObjectKey: "objects/block-1.env",
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply upload intent command")
	intent, err := store.GetUploadIntent("block-1")
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if intent.State != UploadStatePending || !intent.UpdatedAt.Equal(proposedAt) {
		t.Fatalf("intent state/updated_at = %d/%v, want pending/%v", intent.State, intent.UpdatedAt, proposedAt)
	}
}

func TestApplyShardCommandDuplicateRequestIDIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	command := sampleShardCommand()
	testutil.RequireNoErrorf(t, store.ApplyShardCommand(command), "apply command")
	testutil.RequireNoErrorf(t, store.ApplyShardCommand(command), "reapply duplicate command")
	reproposed := sampleShardCommand()
	reproposed.ProposedAt = timestamppb.New(time.Unix(101, 0).UTC())
	testutil.RequireNoErrorf(t, store.ApplyShardCommand(reproposed), "reapply duplicate command with new proposal time")
	transaction, err := store.GetTransaction(identity.Transaction{TenantID: "tenant", TransactionID: "txn"})
	testutil.RequireNoErrorf(t, err, "get transaction")
	if transaction.DocumentCount != 1 {
		t.Fatalf("document count after duplicate command = %d, want 1", transaction.DocumentCount)
	}
	receipts, err := store.ListCommandReceipts()
	testutil.RequireNoErrorf(t, err, "list command receipts")
	if len(receipts) != 1 || receipts[0].CommandID != command.GetCommandId() {
		t.Fatalf("receipts = %#v, want one receipt for duplicate command", receipts)
	}

	conflict := sampleShardCommand()
	conflict.GetCommitDocument().Document.DocumentName = "other.xml"
	if err := store.ApplyShardCommand(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting duplicate error = %v, want %v", err, ErrConflict)
	}
}

func TestApplyUpdateUploadIntentStateCommand(t *testing.T) {
	store := openTestStore(t)
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(sampleUploadIntent("block-1")), "record upload intent")
	proposedAt := time.Unix(31, 0).UTC()
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "upload-complete-1",
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_UpdateUploadIntentState{
			UpdateUploadIntentState: &metastorev1.UpdateUploadIntentStateCommand{
				BlockId: "block-1",
				State:   metastorev1.UploadState_UPLOAD_STATE_UPLOADED,
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply upload intent state command")
	intent, err := store.GetUploadIntent("block-1")
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if intent.State != UploadStateUploaded || intent.HasLastError || !intent.UpdatedAt.Equal(proposedAt) {
		t.Fatalf("intent = %#v, want uploaded without last error at %v", intent, proposedAt)
	}
}

func TestApplyUpdateUploadIntentStateCommandRecordsFailure(t *testing.T) {
	store := openTestStore(t)
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(sampleUploadIntent("block-1")), "record upload intent")
	lastError := "backend throttled"
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "upload-failed-1",
		ProposedAt:    timestamppb.New(time.Unix(32, 0).UTC()),
		Command: &metastorev1.ShardCommand_UpdateUploadIntentState{
			UpdateUploadIntentState: &metastorev1.UpdateUploadIntentStateCommand{
				BlockId:   "block-1",
				State:     metastorev1.UploadState_UPLOAD_STATE_FAILED,
				LastError: &lastError,
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply upload intent failed command")
	intent, err := store.GetUploadIntent("block-1")
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if intent.State != UploadStateFailed || !intent.HasLastError || intent.LastError != lastError {
		t.Fatalf("intent = %#v, want failed with last error", intent)
	}
}

func TestApplyUpdateUploadIntentStateRejectsInvalidState(t *testing.T) {
	store := openTestStore(t)
	testutil.RequireNoErrorf(t, store.RecordUploadIntent(sampleUploadIntent("block-1")), "record upload intent")
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "upload-invalid-1",
		ProposedAt:    timestamppb.New(time.Unix(33, 0).UTC()),
		Command: &metastorev1.ShardCommand_UpdateUploadIntentState{
			UpdateUploadIntentState: &metastorev1.UpdateUploadIntentStateCommand{
				BlockId: "block-1",
				State:   metastorev1.UploadState_UPLOAD_STATE_UNSPECIFIED,
			},
		},
	})
	if err == nil {
		t.Fatal("invalid upload intent state succeeded")
	}
	intent, err := store.GetUploadIntent("block-1")
	testutil.RequireNoErrorf(t, err, "get upload intent")
	if intent.State != UploadStatePending {
		t.Fatalf("intent state = %d, want pending after rejected transition", intent.State)
	}
}

func TestApplyUpdateRestoreStateCommandForDocument(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	documentName := doc.Identity.DocumentName
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "restore-1",
		ProposedAt:    timestamppb.New(time.Unix(30, 0).UTC()),
		Command: &metastorev1.ShardCommand_UpdateRestoreState{
			UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
				TenantId:      doc.Identity.TenantID,
				TransactionId: doc.Identity.TransactionID,
				DocumentName:  &documentName,
				RestoreState:  metastorev1.RestoreState_RESTORE_STATE_RESTORE_PENDING,
				Reason:        "backend restore requested",
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply restore state command")
	got, err := store.HeadDocument(doc.Identity)
	testutil.RequireNoErrorf(t, err, "head document")
	if got.RestoreState != RestoreStateRestorePending || got.Availability != AvailabilityRestorePending {
		t.Fatalf("restore/availability = %d/%d, want restore pending", got.RestoreState, got.Availability)
	}
}

func TestApplyUpdateRestoreStateCommandForTransaction(t *testing.T) {
	store := openTestStore(t)
	first := sampleDocument("invoice.xml", DocumentClassPermanent)
	second := sampleDocument("summary.pdf", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(first), "put first document")
	testutil.RequireNoErrorf(t, store.PutDocument(second), "put second document")
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "restore-transaction-1",
		ProposedAt:    timestamppb.New(time.Unix(30, 0).UTC()),
		Command: &metastorev1.ShardCommand_UpdateRestoreState{
			UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
				TenantId:      first.Identity.TenantID,
				TransactionId: first.Identity.TransactionID,
				RestoreState:  metastorev1.RestoreState_RESTORE_STATE_COLD,
				Reason:        "cooled transaction",
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply transaction restore state command")
	found, err := store.FindDocuments(identity.Transaction{TenantID: "tenant", TransactionID: "txn"}, DocumentFilter{})
	testutil.RequireNoErrorf(t, err, "find documents")
	if len(found) != 2 {
		t.Fatalf("found %d documents, want 2", len(found))
	}
	for _, got := range found {
		if got.RestoreState != RestoreStateCold || got.Availability != AvailabilityCold {
			t.Fatalf("%s restore/availability = %d/%d, want cold", got.Identity.DocumentName, got.RestoreState, got.Availability)
		}
	}
}

func TestApplyUpdateRestoreStateRejectsUnspecifiedState(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	documentName := doc.Identity.DocumentName
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "restore-invalid-1",
		ProposedAt:    timestamppb.New(time.Unix(30, 0).UTC()),
		Command: &metastorev1.ShardCommand_UpdateRestoreState{
			UpdateRestoreState: &metastorev1.UpdateRestoreStateCommand{
				TenantId:      doc.Identity.TenantID,
				TransactionId: doc.Identity.TransactionID,
				DocumentName:  &documentName,
				RestoreState:  metastorev1.RestoreState_RESTORE_STATE_UNSPECIFIED,
				Reason:        "invalid state should not mutate projection",
			},
		},
	})
	if err == nil {
		t.Fatal("apply invalid restore state succeeded")
	}
	got, err := store.HeadDocument(doc.Identity)
	testutil.RequireNoErrorf(t, err, "head document")
	if got.RestoreState != RestoreStateHot || got.Availability != AvailabilityHot {
		t.Fatalf("restore/availability mutated to %d/%d, want hot", got.RestoreState, got.Availability)
	}
}

func TestApplyRecordRepairStateCommand(t *testing.T) {
	store := openTestStore(t)
	proposedAt := time.Unix(40, 0).UTC()
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "repair-1",
		ProposedAt:    timestamppb.New(proposedAt),
		Command: &metastorev1.ShardCommand_RecordRepairState{
			RecordRepairState: &metastorev1.RecordRepairStateCommand{
				TenantId:      "tenant",
				TransactionId: "txn",
				DocumentName:  "invoice.xml",
				PhysicalRef:   "member-a/block-1/64",
				IncidentId:    "incident-1",
				Quarantined:   true,
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply repair state command")
	state, err := store.GetRepairState(identity.Document{TenantID: "tenant", TransactionID: "txn", DocumentName: "invoice.xml"}, "incident-1")
	testutil.RequireNoErrorf(t, err, "get repair state")
	if !state.Quarantined || state.PhysicalRef != "member-a/block-1/64" || !state.UpdatedAt.Equal(proposedAt) {
		t.Fatalf("repair state = %#v, want quarantined physical ref at proposed time", state)
	}
	states, err := store.ListRepairStates()
	testutil.RequireNoErrorf(t, err, "list repair states")
	if len(states) != 1 || states[0].IncidentID != "incident-1" {
		t.Fatalf("repair states = %#v, want recorded incident", states)
	}
}

func TestApplyTombstoneDocumentCommand(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	tombstonedAt := time.Unix(50, 0).UTC()
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "tombstone-1",
		ProposedAt:    timestamppb.New(tombstonedAt),
		Command: &metastorev1.ShardCommand_TombstoneDocument{
			TombstoneDocument: &metastorev1.TombstoneDocumentCommand{
				TenantId:      doc.Identity.TenantID,
				TransactionId: doc.Identity.TransactionID,
				DocumentName:  doc.Identity.DocumentName,
				TombstonedAt:  timestamppb.New(tombstonedAt),
				OperationId:   "018f6d86-7a22-7abc-8def-123456789abc",
			},
		},
	})
	testutil.RequireNoErrorf(t, err, "apply tombstone command")
	got, err := store.HeadDocument(doc.Identity)
	testutil.RequireNoErrorf(t, err, "head document")
	if got.LifecycleState != LifecycleStateTombstoned ||
		got.TombstonedAt == nil ||
		!got.TombstonedAt.Equal(tombstonedAt) ||
		got.TombstoneOperationID != "018f6d86-7a22-7abc-8def-123456789abc" {
		t.Fatalf("tombstoned document = %#v, want tombstone metadata", got)
	}
}

func TestApplyTombstoneDocumentRequiresOperationMetadata(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	testutil.RequireNoErrorf(t, store.PutDocument(doc), "put document")
	err := store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "tombstone-invalid-1",
		ProposedAt:    timestamppb.New(time.Unix(50, 0).UTC()),
		Command: &metastorev1.ShardCommand_TombstoneDocument{
			TombstoneDocument: &metastorev1.TombstoneDocumentCommand{
				TenantId:      doc.Identity.TenantID,
				TransactionId: doc.Identity.TransactionID,
				DocumentName:  doc.Identity.DocumentName,
				OperationId:   "operation-1",
			},
		},
	})
	if err == nil {
		t.Fatal("tombstone without timestamp succeeded")
	}
	err = store.ApplyShardCommand(&metastorev1.ShardCommand{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		CommandId:     "tombstone-invalid-2",
		ProposedAt:    timestamppb.New(time.Unix(51, 0).UTC()),
		Command: &metastorev1.ShardCommand_TombstoneDocument{
			TombstoneDocument: &metastorev1.TombstoneDocumentCommand{
				TenantId:      doc.Identity.TenantID,
				TransactionId: doc.Identity.TransactionID,
				DocumentName:  doc.Identity.DocumentName,
				TombstonedAt:  timestamppb.New(time.Unix(51, 0).UTC()),
			},
		},
	})
	if err == nil {
		t.Fatal("tombstone without operation id succeeded")
	}
	got, err := store.HeadDocument(doc.Identity)
	testutil.RequireNoErrorf(t, err, "head document")
	if got.LifecycleState != LifecycleStateActive || got.TombstonedAt != nil || got.HasTombstoneOperationID {
		t.Fatalf("document mutated after rejected tombstone = %#v", got)
	}
}

func TestFindDocumentsFilters(t *testing.T) {
	store := openTestStore(t)
	testutil.RequireNoErrorf(t, store.PutDocument(sampleDocument("invoice.xml", DocumentClassPermanent)), "put invoice")
	testutil.RequireNoErrorf(t, store.PutDocument(sampleDocument("scratch.tmp", DocumentClassEphemeral)), "put scratch")

	found, err := store.FindDocuments(identity.Transaction{TenantID: "tenant", TransactionID: "txn"}, DocumentFilter{
		DocumentClass:    DocumentClassEphemeral,
		HasDocumentClass: true,
	})
	testutil.RequireNoErrorf(t, err, "find documents")
	if len(found) != 1 || found[0].Identity.DocumentName != "scratch.tmp" {
		t.Fatalf("found = %#v, want only scratch.tmp", found)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close store")
	})
	return store
}

func installTransactionSnapshot(t *testing.T, store *Store, transaction Transaction) {
	t.Helper()
	err := store.ApplyShardSnapshot(&metastorev1.ShardSnapshot{
		SchemaVersion: CurrentSchemaVersion,
		ShardId:       "tenant-txn",
		Membership: &metastorev1.MembershipState{
			Members: []*metastorev1.MembershipMember{
				{
					RaftId:   1,
					MemberId: "member-a",
					Role:     metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER,
				},
			},
		},
		Transactions: []*metastorev1.TransactionRecord{TransactionRecord(transaction)},
	})
	testutil.RequireNoErrorf(t, err, "install transaction snapshot")
}

func sampleDocument(name string, class DocumentClass) Document {
	now := time.Unix(10, 0).UTC()
	logicalSHA := [32]byte{1, 2, 3}
	storedSHA := [32]byte{4, 5, 6}
	frameSHA := [32]byte{7, 8, 9}
	return Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  name,
		},
		DocumentClass:    class,
		PriorityClass:    PriorityClassNormal,
		ContentType:      "application/xml",
		HasContentType:   true,
		Length:           42,
		LogicalSHA256:    logicalSHA,
		StoredSHA256:     storedSHA,
		CreatedByService: "billing-etl",
		WorkflowStage:    "seal",
		HasWorkflowStage: true,
		CreatedAt:        now,
		FinalizedAt:      now,
		Availability:     AvailabilityHot,
		LifecycleState:   LifecycleStateActive,
		RestoreState:     RestoreStateHot,
		UploadState:      UploadStatePending,
		Tags: map[string]string{
			"workflow": "billing",
		},
		Location: blockstore.Record{
			BlockID:       "018f6d86-7a22-7abc-8def-123456789abc",
			StoredOffset:  64,
			StoredLength:  42,
			LogicalSHA256: logicalSHA,
			Frames: []blockstore.FrameRecord{
				{
					FrameOffset:   64,
					SegmentOffset: 64,
					SegmentLength: 42,
					SHA256:        frameSHA,
				},
			},
		},
	}
}

func sampleUploadIntent(blockID string) UploadIntent {
	return UploadIntent{
		BlockID:           blockID,
		BackendObjectKey:  "objects/" + blockID + ".blk",
		IndexObjectKey:    "objects/" + blockID + ".idx",
		EnvelopeObjectKey: "objects/" + blockID + ".env",
		State:             UploadStatePending,
		UpdatedAt:         time.Unix(20, 0).UTC(),
	}
}
