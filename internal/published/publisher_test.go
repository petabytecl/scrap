package published

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestPublishSnapshotWritesSnapshotManifestAndCurrentPointer(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	blockData := []byte("document bytes")
	indexData := []byte("index bytes")
	blockObject, err := store.PutObject(ctx, "objects/block-1.blk", bytes.NewReader(blockData))
	if err != nil {
		t.Fatalf("put block object: %v", err)
	}
	indexObject, err := store.PutObject(ctx, "objects/block-1.idx", bytes.NewReader(indexData))
	if err != nil {
		t.Fatalf("put index object: %v", err)
	}
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument("block-1", blockData)},
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

	first := publishTestSnapshot(t, ctx, store, metadata, "snapshot-1", "manifest-1", 7, 42, time.Unix(100, 0).UTC())
	if first.DocumentCount != 1 {
		t.Fatalf("document count = %d, want 1", first.DocumentCount)
	}
	if first.TransactionCount != 1 {
		t.Fatalf("transaction count = %d, want 1", first.TransactionCount)
	}

	pointer := readCurrentPointerObject(t, ctx, store, first.PointerKey)
	if pointer.GetManifestId() != "manifest-1" || pointer.GetGeneration() != 7 {
		t.Fatalf("pointer = %#v, want manifest-1 generation 7", pointer)
	}
	manifest := readManifestObject(t, ctx, store, first.ManifestKey)
	if manifest.GetSnapshots()[0].GetObjectKey() != first.SnapshotKey ||
		manifest.GetSnapshots()[0].GetLength() != first.SnapshotObject.Length {
		t.Fatalf("manifest snapshot = %#v, want exported snapshot object", manifest.GetSnapshots()[0])
	}
	if len(manifest.GetRequiredObjects()) != 2 ||
		manifest.GetRequiredObjects()[0].GetObjectKey() != blockObject.Key ||
		manifest.GetRequiredObjects()[0].GetKind() != publishedv1.ObjectKind_OBJECT_KIND_BLOCK ||
		manifest.GetRequiredObjects()[1].GetObjectKey() != indexObject.Key ||
		manifest.GetRequiredObjects()[1].GetKind() != publishedv1.ObjectKind_OBJECT_KIND_INDEX {
		t.Fatalf("required objects = %#v, want verified block and index refs", manifest.GetRequiredObjects())
	}

	recordReader := bytes.NewReader(readObject(t, ctx, store, first.SnapshotKey))
	record, err := ReadSnapshotRecord(recordReader)
	if err != nil {
		t.Fatalf("read snapshot record: %v", err)
	}
	location := record.GetDocument().GetLocations()[0]
	if location.GetBackendObjectKey() != blockObject.Key || location.GetIndexObjectKey() != indexObject.Key {
		t.Fatalf("published location = %#v, want backend and index keys", location)
	}
	transactionRecord, err := ReadSnapshotRecord(recordReader)
	if err != nil {
		t.Fatalf("read transaction record: %v", err)
	}
	if transactionRecord.GetTransaction().GetTransactionId() != "tx-a" ||
		transactionRecord.GetTransaction().GetTags()["closed_by"] != "test" {
		t.Fatalf("transaction record = %#v, want published transaction", transactionRecord.GetTransaction())
	}
	if _, err := ReadSnapshotRecord(recordReader); !errors.Is(err, io.EOF) {
		t.Fatalf("next snapshot record error = %v, want EOF", err)
	}

	second := publishTestSnapshot(t, ctx, store, metadata, "snapshot-2", "manifest-2", 8, 43, time.Unix(200, 0).UTC())
	pointer = readCurrentPointerObject(t, ctx, store, second.PointerKey)
	if pointer.GetManifestId() != "manifest-2" || pointer.GetGeneration() != 8 {
		t.Fatalf("updated pointer = %#v, want manifest-2 generation 8", pointer)
	}
}

func TestPublishSnapshotRejectsUnuploadedBlock(t *testing.T) {
	ctx := context.Background()
	store := openPublisherBackend(t)
	metadata := staticSnapshotMetadata{
		documents: []metastore.Document{publishedTestDocument("block-1", []byte("document bytes"))},
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
	if err != nil {
		t.Fatalf("pointer key: %v", err)
	}
	if _, err := store.HeadObject(ctx, pointerKey); !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("pointer head error = %v, want not found", err)
	}
}

func publishTestSnapshot(t *testing.T, ctx context.Context, store backend.MutableStore, metadata SnapshotMetadataSource, snapshotID string, manifestID string, generation uint64, highWatermark uint64, publishedAt time.Time) SnapshotPublication {
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
	if err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	return publication
}

func publishedTestDocument(blockID string, data []byte) metastore.Document {
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
			BlockID:       blockID,
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

func readCurrentPointerObject(t *testing.T, ctx context.Context, store backend.Store, key string) *publishedv1.CurrentPointer {
	t.Helper()
	pointer, err := UnmarshalCurrentPointer(readObject(t, ctx, store, key))
	if err != nil {
		t.Fatalf("unmarshal current pointer: %v", err)
	}
	return pointer
}

func readManifestObject(t *testing.T, ctx context.Context, store backend.Store, key string) *publishedv1.Manifest {
	t.Helper()
	manifest, err := UnmarshalManifest(readObject(t, ctx, store, key))
	if err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return manifest
}

func readObject(t *testing.T, ctx context.Context, store backend.Store, key string) []byte {
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
	if err != nil {
		t.Fatalf("open backend: %v", err)
	}
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
