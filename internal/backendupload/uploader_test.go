package backendupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestUploadBlockStoresReadableBlockObject(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	data := []byte("document bytes inside a block")
	record, err := blocks.Append(ctx, bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)

	result, err := Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
	}.UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "upload block")
	object := result.Block
	if object.Key != intent.BackendObjectKey || object.Length <= record.StoredLength {
		t.Fatalf("uploaded object = %#v, want key %q and full block length", object, intent.BackendObjectKey)
	}
	if result.Index == nil || result.Index.Key != intent.IndexObjectKey {
		t.Fatalf("uploaded index = %#v, want key %q", result.Index, intent.IndexObjectKey)
	}

	var got bytes.Buffer
	length := record.StoredLength
	testutil.RequireNoErrorf(t, store.ReadObjectRange(ctx, intent.BackendObjectKey, backend.Range{Offset: record.StoredOffset, Length: &length}, &got), "read uploaded document range")
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("uploaded document bytes = %q, want %q", got.Bytes(), data)
	}
	got.Reset()
	testutil.RequireNoErrorf(t, store.ReadObjectRange(ctx, intent.IndexObjectKey, backend.Range{}, &got), "read uploaded index")
	if got.String() != "index bytes" {
		t.Fatalf("uploaded index = %q, want index bytes", got.String())
	}
}

func TestUploadBlockIsIdempotent(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("same block")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	uploader := Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("stable index")},
	}
	intent := testUploadIntent(record.BlockID)

	first, err := uploader.UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "first upload")
	second, err := uploader.UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "second upload")
	if second.Block != first.Block ||
		second.Index == nil ||
		first.Index == nil ||
		*second.Index != *first.Index {
		t.Fatalf("second upload = %#v, want first %#v", second, first)
	}
}

func TestUploadBlockVerifiesCompleteObjectSetBeforeSuccess(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("verify object set")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	backendStore := &flakyHeadBackendStore{
		Store: store,
		failures: map[string]int{
			intent.IndexObjectKey: 1,
		},
	}

	_, err = Uploader{
		Backend: backendStore,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
	}.UploadBlock(ctx, intent)
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("upload error = %v, want index verification not found", err)
	}
	if !strings.Contains(err.Error(), intent.IndexObjectKey) {
		t.Fatalf("upload error = %q, want failing index key", err)
	}
	if _, err := store.HeadObject(ctx, intent.BackendObjectKey); err != nil {
		t.Fatalf("head block object after partial upload: %v", err)
	}
	if _, err := store.HeadObject(ctx, intent.IndexObjectKey); err != nil {
		t.Fatalf("head index object after partial upload: %v", err)
	}
}

func TestUploadBlockReportsVerificationMismatchDetails(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("mismatch object set")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	backendStore := corruptHeadBackendStore{
		Store: store,
		key:   intent.IndexObjectKey,
	}

	_, err = Uploader{
		Backend: backendStore,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
	}.UploadBlock(ctx, intent)
	if !errors.Is(err, backend.ErrChecksumMismatch) {
		t.Fatalf("upload error = %v, want checksum mismatch", err)
	}
	if !strings.Contains(err.Error(), intent.IndexObjectKey) || !strings.Contains(err.Error(), "metadata length") {
		t.Fatalf("upload error = %q, want key and mismatched attribute", err)
	}
}

func TestUploadBlockStoresEnvelopeWhenConfigured(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block with envelope")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	result, err := Uploader{
		Backend:  store,
		Source:   LocalBlockSource{Blocks: blocks},
		Index:    staticBlockIndexSource{body: []byte("index bytes")},
		Envelope: LocalBlockEnvelopeSource{CellID: "cell-a"},
	}.UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "upload block")
	if result.Envelope == nil || result.Envelope.Key != intent.EnvelopeObjectKey {
		t.Fatalf("envelope result = %#v, want key %q", result.Envelope, intent.EnvelopeObjectKey)
	}

	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &got), "read uploaded envelope")
	envelope, err := storageformat.UnmarshalEnvelopeRecord(got.Bytes())
	testutil.RequireNoErrorf(t, err, "unmarshal envelope")
	if envelope.GetBlockId() != record.BlockID ||
		envelope.GetKeyId() != localNoopEnvelopeKeyID ||
		envelope.GetAeadAlgorithm() != "none" ||
		len(envelope.GetEnvelopeSha256()) != sha256.Size {
		t.Fatalf("envelope = %#v, want local noop envelope for block", envelope)
	}
}

func TestUploadBlockRequiresIndexSourceWhenIndexObjectKeyIsSet(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block with index")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}

	_, err = Uploader{
		Backend: openTestBackendStore(t),
		Source:  LocalBlockSource{Blocks: blocks},
	}.UploadBlock(ctx, testUploadIntent(record.BlockID))
	if err == nil {
		t.Fatal("expected missing index source error")
	}
}

func TestUploadBlockRequiresEnvelopeSourceWhenEnvelopeObjectKeyIsSet(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("block with envelope")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	_, err = Uploader{
		Backend: openTestBackendStore(t),
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
	}.UploadBlock(ctx, intent)
	if err == nil {
		t.Fatal("expected missing envelope source error")
	}
}

func TestUploadBlockRejectsEncryptedPayloadWithoutIndexObjectKey(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("encrypted block requires index")))
	testutil.RequireNoErrorf(t, err, "append block")
	sealTestCurrentBlock(ctx, t, blocks)
	intent := testUploadIntent(record.BlockID)
	intent.IndexObjectKey = ""
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	_, err = Uploader{
		Backend: openTestBackendStore(t),
		Source:  LocalBlockSource{Blocks: blocks},
		Envelope: TransitBlockEnvelopeSource{
			Transit: cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 1}),
			KeyID:   "transit/backend",
		},
	}.UploadBlock(ctx, intent)
	if err == nil || !strings.Contains(err.Error(), "encrypted block upload requires a block index object key") {
		t.Fatalf("upload error = %v, want missing encrypted index error", err)
	}
}

func TestUploadBlockRequiresBackendObjectKey(t *testing.T) {
	_, err := Uploader{
		Backend: openTestBackendStore(t),
		Source:  staticBlockSource{body: []byte("block")},
	}.UploadBlock(context.Background(), metastore.UploadIntent{BlockID: "block-1"})
	if err == nil {
		t.Fatal("expected missing backend object key error")
	}
}

func TestUploadBlockRequiresSealedLocalBlock(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("open block")))
	testutil.RequireNoErrorf(t, err, "append block")

	_, err = Uploader{
		Backend: openTestBackendStore(t),
		Source:  LocalBlockSource{Blocks: blocks},
	}.UploadBlock(ctx, testUploadIntent(record.BlockID))
	if !errors.Is(err, blockstore.ErrBlockOpen) {
		t.Fatalf("upload error = %v, want %v", err, blockstore.ErrBlockOpen)
	}
}

func TestUploadBlockPropagatesSourceError(t *testing.T) {
	sourceErr := errors.New("block missing")
	_, err := Uploader{
		Backend: openTestBackendStore(t),
		Source:  staticBlockSource{err: sourceErr},
	}.UploadBlock(context.Background(), testUploadIntent("block-1"))
	if !errors.Is(err, sourceErr) {
		t.Fatalf("upload error = %v, want %v", err, sourceErr)
	}
}

func openTestBlockStore(t *testing.T) *blockstore.Store {
	t.Helper()
	store, err := blockstore.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open block store")
	t.Cleanup(func() {
		testutil.RequireNoErrorf(t, store.Close(), "close block store")
	})
	return store
}

func sealTestCurrentBlock(ctx context.Context, t *testing.T, store *blockstore.Store) {
	t.Helper()
	_, err := store.SealCurrent(ctx)
	testutil.RequireNoErrorf(t, err, "seal block")
}

func openTestBackendStore(t *testing.T) *backendfs.Store {
	t.Helper()
	store, err := backendfs.Open(t.TempDir())
	testutil.RequireNoErrorf(t, err, "open backend store")
	return store
}

func readBackendObject(ctx context.Context, t *testing.T, store backend.Store, key string) []byte {
	t.Helper()
	var got bytes.Buffer
	if err := store.ReadObjectRange(ctx, key, backend.Range{}, &got); err != nil {
		t.Fatalf("read backend object %q: %v", key, err)
	}
	return got.Bytes()
}

func testUploadIntent(blockID string) metastore.UploadIntent {
	return metastore.UploadIntent{
		BlockID:          blockID,
		BackendObjectKey: "objects/" + blockID + ".blk",
		IndexObjectKey:   "objects/" + blockID + ".idx",
		State:            metastore.UploadStatePending,
		UpdatedAt:        time.Unix(100, 0).UTC(),
	}
}

type staticBlockSource struct {
	body []byte
	err  error
}

func (s staticBlockSource) OpenBlock(context.Context, string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

type staticBlockIndexSource struct {
	body []byte
	err  error
}

func (s staticBlockIndexSource) OpenBlockIndex(context.Context, metastore.UploadIntent, backend.Object) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

type flakyHeadBackendStore struct {
	backend.Store
	failures map[string]int
}

func (s *flakyHeadBackendStore) HeadObject(ctx context.Context, key string) (backend.Object, error) {
	if s.failures[key] > 0 {
		s.failures[key]--
		return backend.Object{}, backend.ErrNotFound
	}
	return s.Store.HeadObject(ctx, key)
}

type corruptHeadBackendStore struct {
	backend.Store
	key string
}

func (s corruptHeadBackendStore) HeadObject(ctx context.Context, key string) (backend.Object, error) {
	object, err := s.Store.HeadObject(ctx, key)
	if err != nil || key != s.key {
		return object, err
	}
	object.Length++
	return object, nil
}
