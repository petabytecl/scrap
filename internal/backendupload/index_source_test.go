package backendupload

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
)

func TestUploadBlockStoresReadableIndexFromMetastore(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	backendStore := openTestBackendStore(t)
	metadataStore := openTestMetastore(t)
	data := []byte("document bytes for index")
	record, err := blocks.Append(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
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
		StoredSHA256:     record.LogicalSHA256,
		CreatedByService: "billing-etl",
		CreatedAt:        time.Unix(100, 0).UTC(),
		FinalizedAt:      time.Unix(100, 0).UTC(),
		Availability:     metastore.AvailabilityHot,
		LifecycleState:   metastore.LifecycleStateActive,
		RestoreState:     metastore.RestoreStateHot,
		UploadState:      metastore.UploadStatePending,
		Location:         record,
	}
	if err := metadataStore.PutDocument(document); err != nil {
		t.Fatalf("put document metadata: %v", err)
	}
	intent := testUploadIntent(record.BlockID)

	result, err := Uploader{
		Backend: backendStore,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   LocalBlockIndexSource{Documents: metadataStore, ShardID: "local"},
	}.UploadBlock(ctx, intent)
	if err != nil {
		t.Fatalf("upload block: %v", err)
	}
	if result.Index == nil || result.Index.Key != intent.IndexObjectKey {
		t.Fatalf("index result = %#v, want %q", result.Index, intent.IndexObjectKey)
	}

	var indexData bytes.Buffer
	if err := backendStore.ReadObjectRange(ctx, intent.IndexObjectKey, backend.Range{}, &indexData); err != nil {
		t.Fatalf("read uploaded index: %v", err)
	}
	index, err := storageformat.UnmarshalBlockIndex(indexData.Bytes())
	if err != nil {
		t.Fatalf("unmarshal uploaded index: %v", err)
	}
	if index.GetBlockId() != record.BlockID ||
		index.GetShardId() != "local" ||
		index.GetBlockLength() != result.Block.Length ||
		!bytes.Equal(index.GetBlockSha256(), result.Block.SHA256[:]) ||
		len(index.GetDocuments()) != 1 ||
		len(index.GetFrames()) != len(record.Frames) ||
		len(index.GetIndexSha256()) != 32 {
		t.Fatalf("index = %#v, want block/document/frame metadata", index)
	}
	doc := index.GetDocuments()[0]
	if doc.GetStoredOffset() != record.StoredOffset ||
		doc.GetStoredLength() != record.StoredLength ||
		doc.GetLogicalLength() != record.StoredLength ||
		!bytes.Equal(doc.GetLogicalSha256(), record.LogicalSHA256[:]) {
		t.Fatalf("index document = %#v, want written document range", doc)
	}
}

func openTestMetastore(t *testing.T) *metastore.Store {
	t.Helper()
	store, err := metastore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open metastore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close metastore: %v", err)
		}
	})
	return store
}
