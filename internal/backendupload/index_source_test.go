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
		Location:         record,
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

func openTestMetastore(t *testing.T) *metastore.Store {
	t.Helper()
	store, err := metastore.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open metastore")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close metastore")
	})
	return store
}
