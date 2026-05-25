package backendupload

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestUploadBlockStoresReadableIndexFromMetastore(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	backendStore := openTestBackendStore(t)
	metadataStore := openTestMetastore(t)
	data := []byte("document bytes for index")
	record, err := blocks.Append(ctx, bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append block")
	sealTestCurrentBlock(ctx, t, blocks)
	document := metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  "invoice.xml",
		},
		DocumentClass:    metastore.DocumentClassPermanent,
		PriorityClass:    metastore.PriorityClassNormal,
		Length:           record.StoredLength,
		LogicalSHA256:    record.LogicalSHA256,
		StoredSHA256:     record.StoredSHA256,
		CreatedByService: "billing-etl",
		CreatedAt:        time.Unix(100, 0).UTC(),
		FinalizedAt:      time.Unix(100, 0).UTC(),
		Availability:     metastore.AvailabilityHot,
		LifecycleState:   metastore.LifecycleStateActive,
		RestoreState:     metastore.RestoreStateHot,
		UploadState:      metastore.UploadStatePending,
		Location:         metastoreLocationFromBlockRecord(record),
	}
	testutil.RequireNoErrorf(t, metadataStore.PutDocument(document), "put document metadata")
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	result, err := Uploader{
		Backend:  backendStore,
		Source:   LocalBlockSource{Blocks: blocks},
		Envelope: LocalBlockEnvelopeSource{CellID: "local"},
		Index:    LocalBlockIndexSource{Documents: metadataStore, ShardID: "local"},
	}.UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "upload block")
	requireUploadedIndexResult(t, result.Index, intent.IndexObjectKey)

	var indexData bytes.Buffer
	testutil.RequireNoErrorf(t, backendStore.ReadObjectRange(ctx, intent.IndexObjectKey, backend.Range{}, &indexData), "read uploaded index")
	index, err := storageformat.UnmarshalBlockIndex(indexData.Bytes())
	testutil.RequireNoErrorf(t, err, "unmarshal uploaded index")
	requireUploadedBlockIndex(t, index, record, result, intent)
	doc := index.GetDocuments()[0]
	requireUploadedIndexDocument(t, doc, record)
}

func TestBuildBlockIndexSortsDocumentsByPhysicalThenLogicalIdentity(t *testing.T) {
	first := metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant-b",
			TransactionID: "txn-b",
			DocumentName:  "b.xml",
		},
		DocumentClass:    metastore.DocumentClassPermanent,
		PriorityClass:    metastore.PriorityClassNormal,
		Length:           3,
		LogicalSHA256:    [32]byte{1},
		StoredSHA256:     [32]byte{2},
		CreatedByService: "billing",
		CreatedAt:        time.Unix(200, 0).UTC(),
		Tags:             map[string]string{"stage": "seal"},
		Location: metastore.Location{
			BlockID:      "block-1",
			StoredOffset: 10,
			StoredLength: 3,
			Frames: []metastore.FrameRecord{
				{SegmentOffset: 10, SegmentLength: 3, SHA256: [32]byte{3}},
			},
		},
	}
	second := first
	second.Identity.TenantID = "tenant-a"
	second.Identity.TransactionID = "txn-a"
	second.Identity.DocumentName = "a.xml"
	second.CreatedAt = time.Unix(100, 0).UTC()
	second.Location.StoredOffset = 10
	later := first
	later.Identity.DocumentName = "later.xml"
	later.Location.StoredOffset = 20
	later.Location.Frames = []metastore.FrameRecord{
		{SegmentOffset: 20, SegmentLength: 3, SHA256: [32]byte{4}},
	}
	documents := []metastore.Document{later, first, second}

	index, err := buildBlockIndex("block-1", "shard-a", backend.Object{Length: 100, SHA256: [32]byte{9}}, "", documents)
	testutil.RequireNoErrorf(t, err, "build block index")
	testutil.RequireEqualf(t, index.GetCreatedAt().AsTime(), second.CreatedAt, "index created_at")
	testutil.RequireEqualf(t, len(index.GetDocuments()), 3, "index document count")
	if got := index.GetDocuments()[0].GetMetadataBlob(); !bytes.Contains(got, []byte("a.xml")) {
		t.Fatalf("first metadata blob = %x, want a.xml first after sort", got)
	}
	if got := index.GetDocuments()[2].GetStoredOffset(); got != 20 {
		t.Fatalf("last stored offset = %d, want later physical offset", got)
	}
	documents[1].Tags["stage"] = "mutated"
	if bytes.Contains(index.GetDocuments()[1].GetMetadataBlob(), []byte("mutated")) {
		t.Fatal("index metadata changed after source tag mutation")
	}
}

func TestLocalBlockIndexSourceOpenBlockIndexUsesPlainFrameMetadata(t *testing.T) {
	ctx := context.Background()
	metadataStore := openTestMetastore(t)
	document := testIndexDocument("tenant", "txn", "plain.xml", 10, 5, time.Unix(100, 0).UTC())
	testutil.RequireNoErrorf(t, metadataStore.PutDocument(document), "put document metadata")

	reader, err := (LocalBlockIndexSource{Documents: metadataStore, ShardID: "local"}).OpenBlockIndex(ctx, testUploadIntent("block-1"), backend.Object{
		Key:    "objects/block-1.blk",
		Length: 64,
		SHA256: [32]byte{9},
	})
	testutil.RequireNoErrorf(t, err, "open block index")
	defer func() { testutil.RequireNoErrorf(t, reader.Close(), "close block index reader") }()
	data, err := io.ReadAll(reader)
	testutil.RequireNoErrorf(t, err, "read block index")
	index, err := storageformat.UnmarshalBlockIndex(data)
	testutil.RequireNoErrorf(t, err, "unmarshal block index")
	testutil.RequireEqualf(t, len(index.GetFrames()), 1, "frame count")
	testutil.RequireEqualf(t, index.GetFrames()[0].GetEncryptionMode(), storagev1.EncryptionMode_ENCRYPTION_MODE_NONE, "plain frame encryption mode")
}

func TestBuildEncryptedBlockIndexMapsDocumentsToPlaintextFrameRanges(t *testing.T) {
	first := testIndexDocument("tenant-a", "txn-a", "a.xml", 0, 25, time.Unix(200, 0).UTC())
	second := testIndexDocument("tenant-b", "txn-b", "b.xml", 25, 35, time.Unix(100, 0).UTC())
	frames := []EncryptedFrame{
		testEncryptedFrame(0, 0, 16, 0, 32),
		testEncryptedFrame(1, 16, 16, 32, 32),
		testEncryptedFrame(2, 32, 28, 64, 44),
	}

	index, err := buildBlockIndex("block-1", "shard-a", backend.Object{Length: 108, SHA256: [32]byte{9}}, "objects/block-1.env", []metastore.Document{first, second}, BlockIndexOptions{EncryptedFrames: frames})
	testutil.RequireNoErrorf(t, err, "build encrypted index")
	testutil.RequireEqualf(t, index.GetCreatedAt().AsTime(), second.CreatedAt, "index created_at")
	testutil.RequireEqualf(t, len(index.GetDocuments()), 2, "document count")
	testutil.RequireEqualf(t, index.GetDocuments()[0].GetFirstFrameIndex(), uint32(0), "first doc first frame")
	testutil.RequireEqualf(t, index.GetDocuments()[0].GetLastFrameIndex(), uint32(1), "first doc last frame")
	testutil.RequireEqualf(t, index.GetDocuments()[1].GetFirstFrameIndex(), uint32(1), "second doc first frame")
	testutil.RequireEqualf(t, index.GetDocuments()[1].GetLastFrameIndex(), uint32(2), "second doc last frame")
	testutil.RequireEqualf(t, len(index.GetFrames()), 3, "encrypted frame count")
	for _, frame := range index.GetFrames() {
		testutil.RequireEqualf(t, frame.GetEncryptionMode(), storagev1.EncryptionMode_ENCRYPTION_MODE_AES_256_GCM, "frame encryption mode")
		testutil.RequireEqualf(t, len(frame.GetAuthTag()), 16, "frame auth tag length")
	}
}

func TestBuildEncryptedBlockIndexRejectsInvalidEncryptedFrameMetadata(t *testing.T) {
	document := testIndexDocument("tenant", "txn", "uncovered.xml", 100, 10, time.Unix(100, 0).UTC())
	_, err := buildBlockIndex("block-1", "shard-a", backend.Object{Length: 64, SHA256: [32]byte{9}}, "", []metastore.Document{document}, BlockIndexOptions{
		EncryptedFrames: []EncryptedFrame{testEncryptedFrame(0, 0, 32, 0, 48)},
	})
	if err == nil || !strings.Contains(err.Error(), "do not cover document") {
		t.Fatalf("uncovered document error = %v, want coverage failure", err)
	}

	document = testIndexDocument("tenant", "txn", "out-of-order.xml", 0, 10, time.Unix(100, 0).UTC())
	_, err = buildBlockIndex("block-1", "shard-a", backend.Object{Length: 64, SHA256: [32]byte{9}}, "", []metastore.Document{document}, BlockIndexOptions{
		EncryptedFrames: []EncryptedFrame{testEncryptedFrame(1, 0, 32, 0, 48)},
	})
	if err == nil || !strings.Contains(err.Error(), "does not match record order") {
		t.Fatalf("out-of-order frame error = %v, want record order failure", err)
	}

	document = testIndexDocument("tenant", "txn", "overflow.xml", ^uint64(0)-1, 4, time.Unix(100, 0).UTC())
	_, err = buildBlockIndex("block-1", "shard-a", backend.Object{Length: 64, SHA256: [32]byte{9}}, "", []metastore.Document{document}, BlockIndexOptions{
		EncryptedFrames: []EncryptedFrame{testEncryptedFrame(0, 0, 32, 0, 48)},
	})
	if err == nil || !strings.Contains(err.Error(), "stored range overflows") {
		t.Fatalf("overflow document error = %v, want range overflow", err)
	}
}

func requireUploadedIndexResult(t *testing.T, result *backend.Object, key string) {
	t.Helper()
	testutil.RequireNotNilf(t, result, "index result")
	testutil.RequireEqualf(t, result.Key, key, "index object key")
}

func requireUploadedBlockIndex(t *testing.T, index *storagev1.BlockIndex, record blockstore.Record, result UploadResult, intent metastore.UploadIntent) {
	t.Helper()
	testutil.RequireEqualf(t, index.GetBlockId(), record.BlockID, "index block id")
	testutil.RequireEqualf(t, index.GetShardId(), "local", "index shard id")
	testutil.RequireEqualf(t, index.GetBlockLength(), result.Block.Length, "index block length")
	testutil.RequireTruef(t, bytes.Equal(index.GetBlockSha256(), result.Block.SHA256[:]), "index block sha256 = %x, want %x", index.GetBlockSha256(), result.Block.SHA256)
	testutil.RequireEqualf(t, index.GetEnvelopeObjectKey(), intent.EnvelopeObjectKey, "index envelope object key")
	testutil.RequireEqualf(t, len(index.GetDocuments()), 1, "index document count")
	testutil.RequireEqualf(t, len(index.GetFrames()), len(record.Frames), "index frame count")
	testutil.RequireEqualf(t, len(index.GetIndexSha256()), 32, "index checksum length")
}

func requireUploadedIndexDocument(t *testing.T, doc *storagev1.IndexDocumentRecord, record blockstore.Record) {
	t.Helper()
	testutil.RequireEqualf(t, doc.GetStoredOffset(), record.StoredOffset, "index document stored offset")
	testutil.RequireEqualf(t, doc.GetStoredLength(), record.StoredLength, "index document stored length")
	testutil.RequireEqualf(t, doc.GetLogicalLength(), record.StoredLength, "index document logical length")
	testutil.RequireTruef(t, bytes.Equal(doc.GetLogicalSha256(), record.LogicalSHA256[:]), "index document logical sha256")
}

func testIndexDocument(tenantID, transactionID, documentName string, offset, length uint64, createdAt time.Time) metastore.Document {
	return metastore.Document{
		Identity: identity.Document{
			TenantID:      tenantID,
			TransactionID: transactionID,
			DocumentName:  documentName,
		},
		DocumentClass:    metastore.DocumentClassPermanent,
		PriorityClass:    metastore.PriorityClassNormal,
		Length:           length,
		LogicalSHA256:    [32]byte{1},
		StoredSHA256:     [32]byte{2},
		CreatedByService: "billing-etl",
		CreatedAt:        createdAt,
		Location: metastore.Location{
			BlockID:      "block-1",
			StoredOffset: offset,
			StoredLength: length,
			Frames: []metastore.FrameRecord{
				{SegmentOffset: offset, SegmentLength: length, SHA256: [32]byte{3}},
			},
		},
	}
}

func testEncryptedFrame(frameIndex uint32, plaintextOffset, plaintextLength, storedOffset, storedLength uint64) EncryptedFrame {
	return EncryptedFrame{
		FrameIndex:      frameIndex,
		PlaintextOffset: plaintextOffset,
		PlaintextLength: plaintextLength,
		StoredOffset:    storedOffset,
		StoredLength:    storedLength,
		PlaintextSHA256: [32]byte{1},
		StoredSHA256:    [32]byte{2},
		AuthTag:         bytes.Repeat([]byte{1}, 16),
	}
}

func metastoreLocationFromBlockRecord(record blockstore.Record) metastore.Location {
	out := metastore.Location{
		BlockID:       record.BlockID,
		StoredOffset:  record.StoredOffset,
		StoredLength:  record.StoredLength,
		LogicalSHA256: record.LogicalSHA256,
		StoredSHA256:  record.StoredSHA256,
		Frames:        make([]metastore.FrameRecord, 0, len(record.Frames)),
	}
	for _, frame := range record.Frames {
		out.Frames = append(out.Frames, metastore.FrameRecord{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			SHA256:        frame.SHA256,
		})
	}
	return out
}

func openTestMetastore(t *testing.T) *metastore.Store {
	t.Helper()
	store, err := metastore.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open metastore")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close metastore")
	})
	return store
}
