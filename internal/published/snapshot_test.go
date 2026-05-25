package published

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestWriteDocumentSnapshotRecordsOrdersAndMapsLocationObjects(t *testing.T) {
	first := sampleMetastoreDocument("invoice.xml")
	second := sampleMetastoreDocument("summary.pdf")
	second.Location.StoredOffset = 128
	second.Location.BlockID = "block-2"
	otherTenant := sampleMetastoreDocument("adjustment.xml")
	otherTenant.Identity.TenantID = "tenant-b"
	otherTenant.Location.BlockID = "block-3"

	var buf bytes.Buffer
	err := WriteDocumentSnapshotRecords(&buf, SnapshotOptions{
		SourceNamespace: "billing-prod",
		ShardID:         "local",
		HighWatermark:   42,
		LocationObjects: map[string]LocationObjects{
			"block-1": {
				BackendObjectKey:  "blocks/block-1.blk",
				IndexObjectKey:    "blocks/block-1.idx",
				EnvelopeObjectKey: "blocks/block-1.env",
			},
			"block-2": {
				BackendObjectKey: "blocks/block-2.blk",
				IndexObjectKey:   "blocks/block-2.idx",
			},
		},
	}, []metastore.Document{otherTenant, second, first})
	testutil.RequireNoErrorf(t, err, "write snapshot records")

	records := readSnapshotRecords(t, &buf)
	testutil.RequireEqualf(t, len(records), 3, "snapshot record count")
	names := []string{
		records[0].GetDocument().GetDocumentName(),
		records[1].GetDocument().GetDocumentName(),
		records[2].GetDocument().GetDocumentName(),
	}
	testutil.RequireDeepEqualf(t, names, []string{"invoice.xml", "summary.pdf", "adjustment.xml"}, "record names")
	firstDoc := records[0].GetDocument()
	requireSnapshotDocument(t, firstDoc, first)
	location := firstDoc.GetLocations()[0]
	requireSnapshotLocation(t, location, first)
	for _, record := range records {
		requireSnapshotRecordHeader(t, record)
	}
}

func requireSnapshotDocument(t *testing.T, document *publishedv1.PublishedDocument, want metastore.Document) {
	t.Helper()
	testutil.RequireEqualf(t, document.GetTenantId(), want.Identity.TenantID, "snapshot document tenant")
	testutil.RequireEqualf(t, document.GetLength(), want.Length, "snapshot document length")
	testutil.RequireTruef(t, bytes.Equal(document.GetLogicalSha256(), want.LogicalSHA256[:]), "snapshot document sha")
	testutil.RequireEqualf(t, len(document.GetLocations()), 1, "snapshot document location count")
}

func requireSnapshotLocation(t *testing.T, location *publishedv1.PublishedLocation, want metastore.Document) {
	t.Helper()
	testutil.RequireEqualf(t, location.GetBackendObjectKey(), "blocks/block-1.blk", "snapshot backend object key")
	testutil.RequireEqualf(t, location.GetIndexObjectKey(), "blocks/block-1.idx", "snapshot index object key")
	testutil.RequireEqualf(t, location.GetEnvelopeObjectKey(), "blocks/block-1.env", "snapshot envelope object key")
	testutil.RequireEqualf(t, len(location.GetFrames()), 1, "snapshot frame count")
	testutil.RequireTruef(t, bytes.Equal(location.GetFrames()[0].GetSha256(), want.Location.Frames[0].SHA256[:]), "snapshot frame sha")
}

func requireSnapshotRecordHeader(t *testing.T, record *publishedv1.SnapshotRecord) {
	t.Helper()
	testutil.RequireEqualf(t, record.GetSourceNamespace(), "billing-prod", "snapshot source namespace")
	testutil.RequireEqualf(t, record.GetShardId(), "local", "snapshot shard id")
	testutil.RequireEqualf(t, record.GetHighWatermark(), uint64(42), "snapshot high watermark")
}

func TestWriteDocumentSnapshotRecordsProjectsTombstones(t *testing.T) {
	tombstonedAt := time.Unix(200, 0).UTC()
	doc := sampleMetastoreDocument("invoice.xml")
	doc.LifecycleState = metastore.LifecycleStateTombstoned
	doc.TombstonedAt = &tombstonedAt

	var buf bytes.Buffer
	if err := WriteDocumentSnapshotRecords(&buf, SnapshotOptions{
		SourceNamespace: "billing-prod",
		ShardID:         "local",
		HighWatermark:   50,
	}, []metastore.Document{doc}); err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}
	records := readSnapshotRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one tombstone", records)
	}
	tombstone := records[0].GetTombstone()
	if tombstone == nil ||
		tombstone.GetTenantId() != doc.Identity.TenantID ||
		tombstone.GetDocumentName() != doc.Identity.DocumentName ||
		tombstone.GetTombstonedAtIndex() != 50 ||
		!tombstone.GetTombstonedAt().AsTime().Equal(tombstonedAt) {
		t.Fatalf("tombstone = %#v, want projected tombstone", tombstone)
	}
}

func TestWriteMetadataSnapshotRecordsProjectsTransactions(t *testing.T) {
	completedAt := time.Unix(200, 0).UTC()
	first := sampleMetastoreTransaction("txn-a")
	first.State = metastore.TransactionStateCompleted
	first.CompletedAt = &completedAt
	first.Tags = map[string]string{"closed_by": "test"}
	second := sampleMetastoreTransaction("txn-b")

	var buf bytes.Buffer
	if err := WriteMetadataSnapshotRecords(&buf, SnapshotOptions{
		SourceNamespace: "billing-prod",
		ShardID:         "local",
		HighWatermark:   55,
	}, nil, []metastore.Transaction{second, first}); err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}
	records := readSnapshotRecords(t, &buf)
	if len(records) != 2 {
		t.Fatalf("records = %#v, want two transactions", records)
	}
	if records[0].GetTransaction().GetTransactionId() != "txn-a" ||
		records[1].GetTransaction().GetTransactionId() != "txn-b" {
		t.Fatalf("records = %#v, want deterministic transaction order", records)
	}
	transaction := records[0].GetTransaction()
	if transaction.GetState() != uint32(metastore.TransactionStateCompleted) ||
		transaction.GetDocumentCount() != 1 ||
		transaction.GetCompletedAt().AsTime() != completedAt ||
		transaction.GetTags()["closed_by"] != "test" {
		t.Fatalf("transaction = %#v, want projected completed transaction", transaction)
	}
}

func TestWriteDocumentSnapshotRecordsValidatesRequiredOptions(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDocumentSnapshotRecords(nil, SnapshotOptions{SourceNamespace: "billing-prod", ShardID: "local"}, nil); err == nil {
		t.Fatal("nil writer succeeded")
	}
	if err := WriteDocumentSnapshotRecords(&buf, SnapshotOptions{ShardID: "local"}, nil); err == nil {
		t.Fatal("missing source namespace succeeded")
	}
	if err := WriteDocumentSnapshotRecords(&buf, SnapshotOptions{SourceNamespace: "billing-prod"}, nil); err == nil {
		t.Fatal("missing shard id succeeded")
	}
}

func readSnapshotRecords(t *testing.T, reader io.Reader) []*publishedv1.SnapshotRecord {
	t.Helper()
	var records []*publishedv1.SnapshotRecord
	for {
		record, err := ReadSnapshotRecord(reader)
		if errors.Is(err, io.EOF) {
			return records
		}
		testutil.RequireNoErrorf(t, err, "read snapshot record")
		records = append(records, record)
	}
}

func sampleMetastoreDocument(name string) metastore.Document {
	now := time.Unix(100, 0).UTC()
	logicalSHA := [32]byte{1, 2, 3}
	storedSHA := [32]byte{4, 5, 6}
	fingerprint := [16]byte{9, 9, 9}
	frameSHA := [32]byte{7, 8, 9}
	return metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  name,
		},
		DocumentClass:               metastore.DocumentClassPermanent,
		PriorityClass:               metastore.PriorityClassNormal,
		ContentType:                 "application/xml",
		HasContentType:              true,
		Length:                      42,
		LogicalSHA256:               logicalSHA,
		StoredSHA256:                storedSHA,
		DocumentIdentityFingerprint: fingerprint,
		CreatedByService:            "billing-etl",
		WorkflowStage:               "seal",
		HasWorkflowStage:            true,
		CreatedAt:                   now,
		FinalizedAt:                 now,
		Availability:                metastore.AvailabilityHot,
		LifecycleState:              metastore.LifecycleStateActive,
		RestoreState:                metastore.RestoreStateHot,
		UploadState:                 metastore.UploadStateUploaded,
		Tags:                        map[string]string{"workflow": "billing"},
		Location: metastore.Location{
			BlockID:       "block-1",
			StoredOffset:  64,
			StoredLength:  42,
			LogicalSHA256: logicalSHA,
			Frames: []metastore.FrameRecord{
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

func sampleMetastoreTransaction(transactionID string) metastore.Transaction {
	return metastore.Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: transactionID,
		},
		State:                  metastore.TransactionStateOpen,
		DocumentCount:          1,
		PermanentDocumentCount: 1,
		CreatedAt:              time.Unix(100, 0).UTC(),
	}
}
