package published

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestReadSnapshotContentsImportsDocumentsAndUploadIntents(t *testing.T) {
	var snapshot bytes.Buffer
	data := []byte("imported bytes")
	document := publishedTestDocument(data)
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
	err := WriteMetadataSnapshotRecords(&snapshot, SnapshotOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		HighWatermark:   42,
		LocationObjects: map[string]LocationObjects{
			"block-1": {
				BackendObjectKey: "objects/block-1.blk",
				IndexObjectKey:   "objects/block-1.idx",
			},
		},
	}, []metastore.Document{document}, []metastore.Transaction{transaction})
	testutil.RequireNoErrorf(t, err, "write snapshot records")

	contents, err := ReadSnapshotContentsForImport(bytes.NewReader(snapshot.Bytes()), ImportOptions{
		SourceNamespace:      "source-a",
		ShardID:              "shard-a",
		HighWatermark:        42,
		RequireHighWatermark: true,
	})
	testutil.RequireNoErrorf(t, err, "read snapshot contents")
	requireSnapshotImportHeader(t, contents)
	requireSnapshotImportCounts(t, contents)
	imported := contents.Documents[0]
	requireImportedDocument(t, imported, document)
	intent := contents.UploadIntents[0]
	requireImportedIntent(t, intent)
	importedTransaction := contents.Transactions[0]
	requireImportedTransaction(t, importedTransaction, transaction.Identity, completedAt)
}

func requireSnapshotImportHeader(t *testing.T, contents SnapshotContents) {
	t.Helper()
	testutil.RequireEqualf(t, contents.SourceNamespace, "source-a", "snapshot source namespace")
	testutil.RequireEqualf(t, contents.ShardID, "shard-a", "snapshot shard id")
	testutil.RequireEqualf(t, contents.HighWatermark, uint64(42), "snapshot high watermark")
}

func requireSnapshotImportCounts(t *testing.T, contents SnapshotContents) {
	t.Helper()
	testutil.RequireEqualf(t, len(contents.Documents), 1, "snapshot document count")
	testutil.RequireEqualf(t, len(contents.Transactions), 1, "snapshot transaction count")
	testutil.RequireEqualf(t, len(contents.UploadIntents), 1, "snapshot upload intent count")
	testutil.RequireEqualf(t, contents.Tombstones, 0, "snapshot tombstone count")
}

func requireImportedDocument(t *testing.T, imported, document metastore.Document) {
	t.Helper()
	testutil.RequireEqualf(t, imported.Identity, document.Identity, "imported document identity")
	testutil.RequireEqualf(t, imported.Length, document.Length, "imported document length")
	testutil.RequireEqualf(t, imported.Availability, document.Availability, "imported document availability")
	testutil.RequireEqualf(t, imported.LifecycleState, document.LifecycleState, "imported document lifecycle")
	testutil.RequireEqualf(t, imported.UploadState, metastore.UploadStateUploaded, "imported document upload state")
	testutil.RequireEqualf(t, len(imported.Location.Frames), 1, "imported document frame count")
}

func requireImportedIntent(t *testing.T, intent metastore.UploadIntent) {
	t.Helper()
	testutil.RequireEqualf(t, intent.BlockID, "block-1", "imported intent block")
	testutil.RequireEqualf(t, intent.BackendObjectKey, "objects/block-1.blk", "imported intent backend object")
	testutil.RequireEqualf(t, intent.IndexObjectKey, "objects/block-1.idx", "imported intent index object")
	testutil.RequireEqualf(t, intent.State, metastore.UploadStateUploaded, "imported intent state")
}

func requireImportedTransaction(t *testing.T, transaction metastore.Transaction, transactionIdentity identity.Transaction, completedAt time.Time) {
	t.Helper()
	testutil.RequireEqualf(t, transaction.Identity, transactionIdentity, "imported transaction identity")
	testutil.RequireEqualf(t, transaction.State, metastore.TransactionStateCompleted, "imported transaction state")
	testutil.RequireNotNilf(t, transaction.CompletedAt, "imported transaction completed_at")
	testutil.RequireTruef(t, transaction.CompletedAt.Equal(completedAt), "completed_at = %v, want %v", transaction.CompletedAt, completedAt)
	testutil.RequireEqualf(t, transaction.Tags["closed_by"], "import-test", "imported transaction closed_by tag")
}

func TestReadSnapshotContentsRejectsWrongSourceOwnership(t *testing.T) {
	var snapshot bytes.Buffer
	document := publishedTestDocument([]byte("imported bytes"))
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
	testutil.RequireNoErrorf(t, err, "read unconstrained empty snapshot")
	if len(contents.Documents) != 0 || len(contents.Transactions) != 0 || len(contents.UploadIntents) != 0 || contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want empty unconstrained snapshot", contents)
	}
}

func TestReadSnapshotContentsRejectsMalformedPublishedDocument(t *testing.T) {
	var snapshot bytes.Buffer
	document := publishedDocument(publishedTestDocument([]byte("imported bytes")), LocationObjects{
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
	document := publishedDocument(publishedTestDocument([]byte("imported bytes")), LocationObjects{
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
	document := publishedTestDocument(data)
	sum := sha256.Sum256(data)
	err := WriteTailRecord(&tail, &publishedv1.TailRecord{
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
	})
	testutil.RequireNoErrorf(t, err, "write document tail")
	err = WriteTailRecord(&tail, &publishedv1.TailRecord{
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
	})
	testutil.RequireNoErrorf(t, err, "write object tail")

	contents, err := ReadTailContentsForImport(bytes.NewReader(tail.Bytes()), ImportOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		FirstLogIndex:   43,
		LastLogIndex:    44,
		RequireLogRange: true,
	})
	testutil.RequireNoErrorf(t, err, "read tail contents")
	requireTailContents(t, contents, document.Identity)
}

func requireTailContents(t *testing.T, contents TailContents, documentIdentity identity.Document) {
	t.Helper()
	testutil.RequireEqualf(t, contents.SourceNamespace, "source-a", "tail source namespace")
	testutil.RequireEqualf(t, contents.ShardID, "shard-a", "tail shard id")
	testutil.RequireEqualf(t, contents.FirstLogIndex, uint64(43), "tail first log index")
	testutil.RequireEqualf(t, contents.LastLogIndex, uint64(44), "tail last log index")
	testutil.RequireEqualf(t, len(contents.FinalizedDocuments), 1, "tail finalized document count")
	testutil.RequireEqualf(t, len(contents.UploadIntents), 1, "tail upload intent count")
	testutil.RequireEqualf(t, len(contents.ObjectStates), 1, "tail object state count")
	testutil.RequireEqualf(t, contents.FinalizedDocuments[0].Identity, documentIdentity, "tail document identity")
	testutil.RequireEqualf(t, contents.UploadIntents[0].EnvelopeObjectKey, "objects/block-1.env", "tail envelope object key")
	testutil.RequireEqualf(t, contents.ObjectStates[0].GetObject().GetObjectKey(), "objects/block-1.blk", "tail object key")
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
	testutil.RequireNoErrorf(t, err, "read unconstrained empty tail")
	if len(contents.FinalizedDocuments) != 0 ||
		len(contents.Transactions) != 0 ||
		len(contents.UploadIntents) != 0 ||
		len(contents.ObjectStates) != 0 ||
		contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want empty unconstrained tail", contents)
	}
}
