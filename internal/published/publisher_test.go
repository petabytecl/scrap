package published

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestPublishSnapshotWritesSnapshotManifestAndCurrentPointer(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	blockData := []byte("document bytes")
	indexData := []byte("index bytes")
	blockObject, err := store.PutObject(ctx, "objects/block-1.blk", bytes.NewReader(blockData))
	testutil.RequireNoErrorf(t, err, "put block object")
	indexObject, err := store.PutObject(ctx, "objects/block-1.idx", bytes.NewReader(indexData))
	testutil.RequireNoErrorf(t, err, "put index object")
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument(blockData)},
		transactions: []metastore.Transaction{
			{
				Identity: identity.Transaction{
					TenantID:      "tenant-a",
					TransactionID: "tx-a",
				},
				State:                  metastore.TransactionStateCompleted,
				DocumentCount:          1,
				PermanentDocumentCount: 1,
				CreatedAt:              time.Unix(1, 0).UTC(),
				CompletedAt:            timePtr(time.Unix(3, 0).UTC()),
				Tags:                   map[string]string{"closed_by": "test"},
			},
		},
		intents: []metastore.UploadIntent{
			{
				BlockID:          "block-1",
				BackendObjectKey: blockObject.Key,
				IndexObjectKey:   indexObject.Key,
				State:            metastore.UploadStateUploaded,
			},
		},
	}

	first := publishTestSnapshot(ctx, t, store, metadata, "snapshot-1", "manifest-1", 7, 42, time.Unix(100, 0).UTC())
	testutil.RequireEqualf(t, first.DocumentCount, 1, "document count")
	testutil.RequireEqualf(t, first.TransactionCount, 1, "transaction count")

	pointer := readCurrentPointerObject(ctx, t, store, first.PointerKey)
	testutil.RequireEqualf(t, pointer.GetManifestId(), "manifest-1", "pointer manifest id")
	testutil.RequireEqualf(t, pointer.GetGeneration(), uint64(7), "pointer generation")
	manifest := readManifestObject(ctx, t, store, first.ManifestKey)
	requirePublishedManifestObjects(t, manifest, first, blockObject.Key, indexObject.Key)

	recordReader := bytes.NewReader(readObject(ctx, t, store, first.SnapshotKey))
	record, err := ReadSnapshotRecord(recordReader)
	testutil.RequireNoErrorf(t, err, "read snapshot record")
	location := record.GetDocument().GetLocations()[0]
	testutil.RequireEqualf(t, location.GetBackendObjectKey(), blockObject.Key, "published backend object key")
	testutil.RequireEqualf(t, location.GetIndexObjectKey(), indexObject.Key, "published index object key")
	transactionRecord, err := ReadSnapshotRecord(recordReader)
	testutil.RequireNoErrorf(t, err, "read transaction record")
	testutil.RequireEqualf(t, transactionRecord.GetTransaction().GetTransactionId(), "tx-a", "transaction id")
	testutil.RequireEqualf(t, transactionRecord.GetTransaction().GetTags()["closed_by"], "test", "transaction closed_by tag")
	_, err = ReadSnapshotRecord(recordReader)
	testutil.RequireEOFf(t, err, "next snapshot record")

	second := publishTestSnapshot(ctx, t, store, metadata, "snapshot-2", "manifest-2", 8, 43, time.Unix(200, 0).UTC())
	pointer = readCurrentPointerObject(ctx, t, store, second.PointerKey)
	testutil.RequireEqualf(t, pointer.GetManifestId(), "manifest-2", "updated pointer manifest id")
	testutil.RequireEqualf(t, pointer.GetGeneration(), uint64(8), "updated pointer generation")
}

func requirePublishedManifestObjects(t *testing.T, manifest *publishedv1.Manifest, publication SnapshotPublication, blockKey, indexKey string) {
	t.Helper()
	testutil.RequireEqualf(t, manifest.GetSnapshots()[0].GetObjectKey(), publication.SnapshotKey, "manifest snapshot object key")
	testutil.RequireEqualf(t, manifest.GetSnapshots()[0].GetLength(), publication.SnapshotObject.Length, "manifest snapshot length")
	required := manifest.GetRequiredObjects()
	testutil.RequireEqualf(t, len(required), 2, "required object count")
	testutil.RequireEqualf(t, required[0].GetObjectKey(), blockKey, "required block object key")
	testutil.RequireEqualf(t, required[0].GetKind(), publishedv1.ObjectKind_OBJECT_KIND_BLOCK, "required block kind")
	testutil.RequireEqualf(t, required[1].GetObjectKey(), indexKey, "required index object key")
	testutil.RequireEqualf(t, required[1].GetKind(), publishedv1.ObjectKind_OBJECT_KIND_INDEX, "required index kind")
}

func TestPublishSnapshotRejectsUnuploadedBlock(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument([]byte("document bytes"))},
		intents: []metastore.UploadIntent{
			{
				BlockID:          "block-1",
				BackendObjectKey: "objects/block-1.blk",
				State:            metastore.UploadStatePending,
			},
		},
	}

	_, err := PublishSnapshot(ctx, SnapshotPublishOptions{
		Backend:         store,
		Metadata:        metadata,
		CellID:          "cell-a",
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		SnapshotID:      "snapshot-1",
		ManifestID:      "manifest-1",
		Generation:      7,
		HighWatermark:   42,
		PublishedAt:     time.Unix(100, 0).UTC(),
	})
	if err == nil {
		t.Fatal("publish snapshot succeeded for unuploaded block")
	}
	pointerKey, err := CurrentPointerObjectKey("cell-a")
	testutil.RequireNoErrorf(t, err, "pointer key")
	if _, err := store.HeadObject(ctx, pointerKey); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("pointer head error = %v, want not found", err)
	}
}

func publishTestSnapshot(ctx context.Context, t *testing.T, store backend.MutableStore, metadata SnapshotMetadataSource, snapshotID, manifestID string, generation, highWatermark uint64, publishedAt time.Time) SnapshotPublication {
	t.Helper()
	publication, err := PublishSnapshot(ctx, SnapshotPublishOptions{
		Backend:         store,
		Metadata:        metadata,
		CellID:          "cell-a",
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		SnapshotID:      snapshotID,
		ManifestID:      manifestID,
		Generation:      generation,
		HighWatermark:   highWatermark,
		PublishedAt:     publishedAt,
		ProducerBuild:   "test",
		ProducerSchema:  "test-schema",
	})
	testutil.RequireNoErrorf(t, err, "publish snapshot")
	return publication
}

func publishedTestDocument(data []byte) metastore.Document {
	sum := sha256.Sum256(data)
	return metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant-a",
			TransactionID: "tx-a",
			DocumentName:  "doc-a.xml",
		},
		DocumentClass:    metastore.DocumentClassPermanent,
		PriorityClass:    metastore.PriorityClassNormal,
		Length:           uint64(len(data)),
		LogicalSHA256:    sum,
		StoredSHA256:     sum,
		CreatedByService: "billing-etl",
		CreatedAt:        time.Unix(1, 0).UTC(),
		FinalizedAt:      time.Unix(2, 0).UTC(),
		Availability:     metastore.AvailabilityHot,
		LifecycleState:   metastore.LifecycleStateActive,
		Location: blockstore.Record{
			BlockID:       "block-1",
			StoredOffset:  0,
			StoredLength:  uint64(len(data)),
			LogicalSHA256: sum,
			Frames: []blockstore.FrameRecord{
				{
					FrameOffset:   0,
					SegmentOffset: 0,
					SegmentLength: uint64(len(data)),
					SHA256:        sum,
				},
			},
		},
	}
}

func readCurrentPointerObject(ctx context.Context, t *testing.T, store backend.Store, key string) *publishedv1.CurrentPointer {
	t.Helper()
	pointer, err := UnmarshalCurrentPointer(readObject(ctx, t, store, key))
	testutil.RequireNoErrorf(t, err, "unmarshal current pointer")
	return pointer
}

func readManifestObject(ctx context.Context, t *testing.T, store backend.Store, key string) *publishedv1.Manifest {
	t.Helper()
	manifest, err := UnmarshalManifest(readObject(ctx, t, store, key))
	testutil.RequireNoErrorf(t, err, "unmarshal manifest")
	return manifest
}

func readObject(ctx context.Context, t *testing.T, store backend.Store, key string) []byte {
	t.Helper()
	var got bytes.Buffer
	if err := store.ReadObjectRange(ctx, key, backend.Range{}, &got); err != nil {
		t.Fatalf("read object %q: %v", key, err)
	}
	return got.Bytes()
}

func openPublisherBackend(t *testing.T) *backendfs.Store {
	t.Helper()
	store, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend")
	return store
}

type staticSnapshotMetadata struct {
	documents    []metastore.Document
	transactions []metastore.Transaction
	intents      []metastore.UploadIntent
}

func (s staticSnapshotMetadata) ListDocuments(metastore.DocumentFilter) ([]metastore.Document, error) {
	return append([]metastore.Document(nil), s.documents...), nil
}

func (s staticSnapshotMetadata) ListUploadIntents() ([]metastore.UploadIntent, error) {
	return append([]metastore.UploadIntent(nil), s.intents...), nil
}

func (s staticSnapshotMetadata) ListTransactions() ([]metastore.Transaction, error) {
	return append([]metastore.Transaction(nil), s.transactions...), nil
}

func timePtr(value time.Time) *time.Time {
	return &value
}
