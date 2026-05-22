package backendupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	backendfs "github.com/petabytecl/scrap/internal/backend/fs"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
)

func TestUploadBlockStoresReadableBlockObject(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	data := []byte("document bytes inside a block")
	record, err := blocks.Append(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	intent := testUploadIntent(record.BlockID)

	object, err := Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
	}.UploadBlock(ctx, intent)
	if err != nil {
		t.Fatalf("upload block: %v", err)
	}
	if object.Key != intent.BackendObjectKey || object.Length <= record.StoredLength {
		t.Fatalf("uploaded object = %#v, want key %q and full block length", object, intent.BackendObjectKey)
	}

	var got bytes.Buffer
	length := record.StoredLength
	if err := store.ReadObjectRange(ctx, intent.BackendObjectKey, backend.Range{Offset: record.StoredOffset, Length: &length}, &got); err != nil {
		t.Fatalf("read uploaded document range: %v", err)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("uploaded document bytes = %q, want %q", got.Bytes(), data)
	}
}

func TestUploadBlockIsIdempotent(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("same block")))
	if err != nil {
		t.Fatalf("append block: %v", err)
	}
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	uploader := Uploader{Backend: store, Source: LocalBlockSource{Blocks: blocks}}
	intent := testUploadIntent(record.BlockID)

	first, err := uploader.UploadBlock(ctx, intent)
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	second, err := uploader.UploadBlock(ctx, intent)
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if second != first {
		t.Fatalf("second upload = %#v, want first %#v", second, first)
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
	if err != nil {
		t.Fatalf("append block: %v", err)
	}

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
	if err != nil {
		t.Fatalf("open block store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close block store: %v", err)
		}
	})
	return store
}

func openTestBackendStore(t *testing.T) *backendfs.Store {
	t.Helper()
	store, err := backendfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backend store: %v", err)
	}
	return store
}

func testUploadIntent(blockID string) metastore.UploadIntent {
	return metastore.UploadIntent{
		BlockID:          blockID,
		BackendObjectKey: "objects/" + blockID + ".blk",
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
