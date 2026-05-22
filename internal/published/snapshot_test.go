package published

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/blockstore"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
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
	if err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}

	records := readSnapshotRecords(t, &buf)
	if len(records) != 3 {
		t.Fatalf("records = %#v, want 3", records)
	}
	names := []string{
		records[0].GetDocument().GetDocumentName(),
		records[1].GetDocument().GetDocumentName(),
		records[2].GetDocument().GetDocumentName(),
	}
	if names[0] != "invoice.xml" || names[1] != "summary.pdf" || names[2] != "adjustment.xml" {
		t.Fatalf("record names = %#v, want deterministic identity order", names)
	}
	firstDoc := records[0].GetDocument()
	if firstDoc.GetTenantId() != first.Identity.TenantID ||
		firstDoc.GetLength() != first.Length ||
		!bytes.Equal(firstDoc.GetLogicalSha256(), first.LogicalSHA256[:]) ||
		len(firstDoc.GetLocations()) != 1 {
		t.Fatalf("first document = %#v, want projected metastore document", firstDoc)
	}
	location := firstDoc.GetLocations()[0]
	if location.GetBackendObjectKey() != "blocks/block-1.blk" ||
		location.GetIndexObjectKey() != "blocks/block-1.idx" ||
		location.GetEnvelopeObjectKey() != "blocks/block-1.env" ||
		len(location.GetFrames()) != 1 ||
		!bytes.Equal(location.GetFrames()[0].GetSha256(), first.Location.Frames[0].SHA256[:]) {
		t.Fatalf("location = %#v, want backend object keys and frame checksum", location)
	}
	for _, record := range records {
		if record.GetSourceNamespace() != "billing-prod" ||
			record.GetShardId() != "local" ||
			record.GetHighWatermark() != 42 {
			t.Fatalf("record header = %#v, want shared snapshot metadata", record)
		}
	}
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
		if err != nil {
			t.Fatalf("read snapshot record: %v", err)
		}
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
		Location: blockstore.Record{
			BlockID:       "block-1",
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
