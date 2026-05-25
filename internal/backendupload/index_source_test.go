package backendupload

import (
	"bytes"
	"context"
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
