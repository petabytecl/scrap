package published

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReadSnapshotContentsImportsDocumentsAndUploadIntents(t *testing.T) {
	var snapshot bytes.Buffer
	data := []byte("imported bytes")
	document := publishedTestDocument("block-1", data)
	completedAt := time.Unix(300, 0).UTC()
	transaction := metastore.Transaction{
		Identity: identity.Transaction{
			TenantID:      document.Identity.TenantID,
			TransactionID: document.Identity.TransactionID,
		},
		State:                  metastore.TransactionStateCompleted,
		DocumentCount:          1,
		PermanentDocumentCount: 1,
		CreatedAt:              time.Unix(100, 0).UTC(),
		CompletedAt:            &completedAt,
		Tags:                   map[string]string{"closed_by": "import-test"},
	}
	if err := WriteMetadataSnapshotRecords(&snapshot, SnapshotOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		HighWatermark:   42,
		LocationObjects: map[string]LocationObjects{
			"block-1": {
				BackendObjectKey: "objects/block-1.blk",
				IndexObjectKey:   "objects/block-1.idx",
			},
		},
	}, []metastore.Document{document}, []metastore.Transaction{transaction}); err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}

	contents, err := ReadSnapshotContentsForImport(bytes.NewReader(snapshot.Bytes()), ImportOptions{
		SourceNamespace:      "source-a",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	if err != nil {
		t.Fatalf("read snapshot contents: %v", err)
	}
	if contents.SourceNamespace != "source-a" ||
		contents.ShardID != "shard-a" ||
		contents.HighWatermark != 42 {
		t.Fatalf("contents header = %#v, want source/shard/high-watermark from snapshot", contents)
	}
	if len(contents.Documents) != 1 ||
		len(contents.Transactions) != 1 ||
		len(contents.UploadIntents) != 1 ||
		contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want one document, transaction, and upload intent", contents)
	}
	imported := contents.Documents[0]
	if imported.Identity != document.Identity ||
		imported.Length != document.Length ||
		imported.Availability != document.Availability ||
		imported.LifecycleState != document.LifecycleState ||
		imported.UploadState != metastore.UploadStateUploaded ||
		len(imported.Location.Frames) != 1 {
		t.Fatalf("imported document = %#v, want published document metadata", imported)
	}
	intent := contents.UploadIntents[0]
	if intent.BlockID != "block-1" ||
		intent.BackendObjectKey != "objects/block-1.blk" ||
		intent.IndexObjectKey != "objects/block-1.idx" ||
		intent.State != metastore.UploadStateUploaded {
		t.Fatalf("imported intent = %#v, want uploaded backend refs", intent)
	}
	importedTransaction := contents.Transactions[0]
	if importedTransaction.Identity != transaction.Identity ||
		importedTransaction.State != metastore.TransactionStateCompleted ||
		importedTransaction.CompletedAt == nil ||
		!importedTransaction.CompletedAt.Equal(completedAt) ||
		importedTransaction.Tags["closed_by"] != "import-test" {
		t.Fatalf("imported transaction = %#v, want completed transaction metadata", importedTransaction)
	}
}

func TestReadSnapshotContentsRejectsWrongSourceOwnership(t *testing.T) {
	var snapshot bytes.Buffer
	document := publishedTestDocument("block-1", []byte("imported bytes"))
	if err := WriteMetadataSnapshotRecords(&snapshot, SnapshotOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		HighWatermark:   42,
		LocationObjects: map[string]LocationObjects{
			"block-1": {
				BackendObjectKey: "objects/block-1.blk",
			},
		},
	}, []metastore.Document{document}, nil); err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}

	_, err := ReadSnapshotContentsForImport(bytes.NewReader(snapshot.Bytes()), ImportOptions{
		SourceNamespace:      "other-source",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	if !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrSourceMismatch)
	}
}

func TestReadSnapshotContentsRejectsEmptyConstrainedImport(t *testing.T) {
	_, err := ReadSnapshotContentsForImport(bytes.NewReader(nil), ImportOptions{
		SourceNamespace:      "source-a",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidArtifact)
	}

	contents, err := ReadSnapshotContents(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("read unconstrained empty snapshot: %v", err)
	}
	if len(contents.Documents) != 0 || len(contents.Transactions) != 0 || len(contents.UploadIntents) != 0 || contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want empty unconstrained snapshot", contents)
	}
}

func TestReadSnapshotContentsRejectsMalformedPublishedDocument(t *testing.T) {
	var snapshot bytes.Buffer
	document := publishedDocument(publishedTestDocument("block-1", []byte("imported bytes")), LocationObjects{
		BackendObjectKey: "objects/block-1.blk",
	})
	document.LogicalSha256 = []byte{1, 2, 3}
	if err := WriteSnapshotRecord(&snapshot, &publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "source-a",
		ShardId:         "shard-a",
		HighWatermark:   42,
		Record: &publishedv1.SnapshotRecord_Document{
			Document: document,
		},
	}); err != nil {
		t.Fatalf("write snapshot record: %v", err)
	}

	_, err := ReadSnapshotContentsForImport(bytes.NewReader(snapshot.Bytes()), ImportOptions{
		SourceNamespace:      "source-a",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidArtifact)
	}
}

func TestReadSnapshotContentsRejectsUnsupportedLocationFormatVersion(t *testing.T) {
	var snapshot bytes.Buffer
	document := publishedDocument(publishedTestDocument("block-1", []byte("imported bytes")), LocationObjects{
		BackendObjectKey: "objects/block-1.blk",
	})
	document.Locations[0].FormatVersion = CurrentLocationFormatVersion + 1
	if err := WriteSnapshotRecord(&snapshot, &publishedv1.SnapshotRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "source-a",
		ShardId:         "shard-a",
		HighWatermark:   42,
		Record: &publishedv1.SnapshotRecord_Document{
			Document: document,
		},
	}); err != nil {
		t.Fatalf("write snapshot record: %v", err)
	}

	_, err := ReadSnapshotContentsForImport(bytes.NewReader(snapshot.Bytes()), ImportOptions{
		SourceNamespace:      "source-a",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	if !errors.Is(err, ErrUnsupportedFormatVersion) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedFormatVersion)
	}
}

func TestReadTailContentsImportsFinalizedDocumentsAndObjectStates(t *testing.T) {
	var tail bytes.Buffer
	data := []byte("imported bytes")
	document := publishedTestDocument("block-1", data)
	sum := sha256.Sum256(data)
	if err := WriteTailRecord(&tail, &publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "source-a",
		ShardId:         "shard-a",
		LogIndex:        43,
		Mutation: &publishedv1.TailRecord_FinalizedDocument{
			FinalizedDocument: publishedDocument(document, LocationObjects{
				BackendObjectKey:  "objects/block-1.blk",
				IndexObjectKey:    "objects/block-1.idx",
				EnvelopeObjectKey: "objects/block-1.env",
			}),
		},
	}); err != nil {
		t.Fatalf("write document tail: %v", err)
	}
	if err := WriteTailRecord(&tail, &publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "source-a",
		ShardId:         "shard-a",
		LogIndex:        44,
		Mutation: &publishedv1.TailRecord_ObjectState{
			ObjectState: &publishedv1.PublishedObjectState{
				Object: &publishedv1.ObjectRef{
					Kind:      publishedv1.ObjectKind_OBJECT_KIND_BLOCK,
					ObjectKey: "objects/block-1.blk",
					Length:    uint64(len(data)),
					Sha256:    sum[:],
				},
				AvailableAtIndex: 43,
			},
		},
	}); err != nil {
		t.Fatalf("write object tail: %v", err)
	}

	contents, err := ReadTailContentsForImport(bytes.NewReader(tail.Bytes()), ImportOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		FirstLogIndex:   43,
		LastLogIndex:    44,
		RequireLogRange: true,
	})
	if err != nil {
		t.Fatalf("read tail contents: %v", err)
	}
	if contents.SourceNamespace != "source-a" ||
		contents.ShardID != "shard-a" ||
		contents.FirstLogIndex != 43 ||
		contents.LastLogIndex != 44 ||
		len(contents.FinalizedDocuments) != 1 ||
		len(contents.UploadIntents) != 1 ||
		len(contents.ObjectStates) != 1 {
		t.Fatalf("tail contents = %#v, want finalized document, intent, and object state", contents)
	}
	if contents.FinalizedDocuments[0].Identity != document.Identity ||
		contents.UploadIntents[0].EnvelopeObjectKey != "objects/block-1.env" ||
		contents.ObjectStates[0].GetObject().GetObjectKey() != "objects/block-1.blk" {
		t.Fatalf("tail contents = %#v, want imported published mutations", contents)
	}
}

func TestReadTailContentsRejectsWrongSourceOwnership(t *testing.T) {
	var tail bytes.Buffer
	if err := WriteTailRecord(&tail, &publishedv1.TailRecord{
		SchemaVersion:   CurrentSchemaVersion,
		SourceNamespace: "source-a",
		ShardId:         "shard-a",
		LogIndex:        43,
		Mutation: &publishedv1.TailRecord_Tombstone{
			Tombstone: &publishedv1.PublishedTombstone{
				TenantId:          "tenant-a",
				TransactionId:     "tx-a",
				TombstonedAtIndex: 43,
				TombstonedAt:      timestamppb.New(time.Unix(43, 0).UTC()),
			},
		},
	}); err != nil {
		t.Fatalf("write tail record: %v", err)
	}

	_, err := ReadTailContentsForImport(bytes.NewReader(tail.Bytes()), ImportOptions{
		SourceNamespace: "source-a",
		ShardID:         "other-shard",
		FirstLogIndex:   43,
		LastLogIndex:    43,
		RequireLogRange: true,
	})
	if !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrSourceMismatch)
	}
}

func TestReadTailContentsRejectsEmptyConstrainedImport(t *testing.T) {
	_, err := ReadTailContentsForImport(bytes.NewReader(nil), ImportOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		FirstLogIndex:   43,
		LastLogIndex:    44,
		RequireLogRange: true,
	})
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidArtifact)
	}

	contents, err := ReadTailContents(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("read unconstrained empty tail: %v", err)
	}
	if len(contents.FinalizedDocuments) != 0 ||
		len(contents.Transactions) != 0 ||
		len(contents.UploadIntents) != 0 ||
		len(contents.ObjectStates) != 0 ||
		contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want empty unconstrained tail", contents)
	}
}
