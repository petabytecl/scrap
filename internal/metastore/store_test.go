package metastore

import (
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPutHeadFindDocument(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)

	if err := store.PutDocument(doc); err != nil {
		t.Fatalf("put document: %v", err)
	}

	got, err := store.HeadDocument(doc.Identity)
	if err != nil {
		t.Fatalf("head document: %v", err)
	}
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
	if err != nil {
		t.Fatalf("find documents: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d documents, want 1", len(found))
	}
}

func TestPutDocumentIsIdempotentAndCountsOnce(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)

	if err := store.PutDocument(doc); err != nil {
		t.Fatalf("put document: %v", err)
	}
	if err := store.PutDocument(doc); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}

	transaction, err := store.GetTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	})
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
	if transaction.DocumentCount != 1 || transaction.PermanentDocumentCount != 1 {
		t.Fatalf("counts = %#v, want one permanent document", transaction)
	}
}

func TestPutDocumentRejectsConflictingReplay(t *testing.T) {
	store := openTestStore(t)
	doc := sampleDocument("invoice.xml", DocumentClassPermanent)
	if err := store.PutDocument(doc); err != nil {
		t.Fatalf("put document: %v", err)
	}

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
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	doc := sampleDocument("invoice.xml", DocumentClassEphemeral)
	if err := store.PutDocument(doc); err != nil {
		t.Fatalf("put document: %v", err)
	}
	completedAt := doc.CreatedAt.Add(time.Minute)
	if _, err := store.CompleteTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	}, completedAt, map[string]string{"closed_by": "test"}); err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	transaction, err := reopened.GetTransaction(identity.Transaction{
		TenantID:      doc.Identity.TenantID,
		TransactionID: doc.Identity.TransactionID,
	})
	if err != nil {
		t.Fatalf("get transaction: %v", err)
	}
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

func TestRecordUploadIntentPersistsAndLists(t *testing.T) {
	store := openTestStore(t)
	first := sampleUploadIntent("block-1")
	second := sampleUploadIntent("block-2")
	if err := store.RecordUploadIntent(first); err != nil {
		t.Fatalf("record first upload intent: %v", err)
	}
	if err := store.RecordUploadIntent(second); err != nil {
		t.Fatalf("record second upload intent: %v", err)
	}

	got, err := store.GetUploadIntent(first.BlockID)
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	if got != first {
		t.Fatalf("upload intent = %#v, want %#v", got, first)
	}
	intents, err := store.ListUploadIntents()
	if err != nil {
		t.Fatalf("list upload intents: %v", err)
	}
	if len(intents) != 2 || intents[0].BlockID != "block-1" || intents[1].BlockID != "block-2" {
		t.Fatalf("upload intents = %#v, want block-1 then block-2", intents)
	}
}

func TestRecordUploadIntentIsIdempotentAndConflictsOnDifferentKeys(t *testing.T) {
	store := openTestStore(t)
	intent := sampleUploadIntent("block-1")
	if err := store.RecordUploadIntent(intent); err != nil {
		t.Fatalf("record upload intent: %v", err)
	}
	if err := store.RecordUploadIntent(intent); err != nil {
		t.Fatalf("idempotent upload intent: %v", err)
	}
	conflict := intent
	conflict.BackendObjectKey = "other-object"
	if err := store.RecordUploadIntent(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want %v", err, ErrConflict)
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
	if err != nil {
		t.Fatalf("apply upload intent command: %v", err)
	}
	intent, err := store.GetUploadIntent("block-1")
	if err != nil {
		t.Fatalf("get upload intent: %v", err)
	}
	if intent.State != UploadStatePending || !intent.UpdatedAt.Equal(proposedAt) {
		t.Fatalf("intent state/updated_at = %d/%v, want pending/%v", intent.State, intent.UpdatedAt, proposedAt)
	}
}

func TestFindDocumentsFilters(t *testing.T) {
	store := openTestStore(t)
	if err := store.PutDocument(sampleDocument("invoice.xml", DocumentClassPermanent)); err != nil {
		t.Fatalf("put invoice: %v", err)
	}
	if err := store.PutDocument(sampleDocument("scratch.tmp", DocumentClassEphemeral)); err != nil {
		t.Fatalf("put scratch: %v", err)
	}

	found, err := store.FindDocuments(identity.Transaction{TenantID: "tenant", TransactionID: "txn"}, DocumentFilter{
		DocumentClass:    DocumentClassEphemeral,
		HasDocumentClass: true,
	})
	if err != nil {
		t.Fatalf("find documents: %v", err)
	}
	if len(found) != 1 || found[0].Identity.DocumentName != "scratch.tmp" {
		t.Fatalf("found = %#v, want only scratch.tmp", found)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
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
