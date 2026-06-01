package index_test

import (
	"errors"
	"io"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestConfirmedUploadRoundTrip(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	confirmed := confirmedUploadCatalogFixture()
	if err := idx.PutConfirmedUpload(confirmed); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	got, err := idx.GetConfirmedUpload(42)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("ConfirmedUpload = %+v, want %+v", got, confirmed)
	}
}

func TestConfirmedUploadRequiresRestoreMetadata(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	confirmed := confirmedUploadCatalogFixture()
	confirmed.IndexObject.ValidationToken = ""
	err = idx.PutConfirmedUpload(confirmed)
	if err == nil {
		t.Fatal("PutConfirmedUpload succeeded without index validation token")
	}

	if _, getErr := idx.GetConfirmedUpload(42); !errors.Is(getErr, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", getErr)
	}
}

func TestConfirmedUploadsIteratesByBlockID(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	putConfirmedUpload(t, idx, 3)
	putConfirmedUpload(t, idx, 1)
	putConfirmedUpload(t, idx, 2)

	iter, err := idx.ConfirmedUploads()
	if err != nil {
		t.Fatalf("ConfirmedUploads: %v", err)
	}

	assertConfirmedUpload(t, nextConfirmedUpload(t, iter, "first"), 1)
	assertConfirmedUpload(t, nextConfirmedUpload(t, iter, "second"), 2)
	assertConfirmedUpload(t, nextConfirmedUpload(t, iter, "third"), 3)

	if _, err := iter.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final error = %v, want EOF", err)
	}
}

func confirmedUploadCatalogFixture() index.ConfirmedUpload {
	return index.ConfirmedUpload{
		BlockID:         42,
		ShardID:         7,
		ConfirmedAtUs:   1716700001000000,
		SealedSizeBytes: 67108864,
		BlockObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/000000000000002a.blk",
			SizeBytes:       67108864,
			ValidationToken: catalogValidationValue("block"),
		},
		IndexObject: index.BackendObjectMetadata{
			Key:             "cell-a/shards/0000000000000007/000000000000002a.idx",
			SizeBytes:       4096,
			ValidationToken: catalogValidationValue("index"),
		},
	}
}

func catalogValidationValue(kind string) string {
	return kind + "-validation"
}

func putConfirmedUpload(t *testing.T, idx *index.Index, blockID uint64) {
	t.Helper()

	confirmed := confirmedUploadCatalogFixture()
	confirmed.BlockID = blockID
	confirmed.ConfirmedAtUs += confirmedUploadOffsetForTest(t, blockID)
	confirmed.SealedSizeBytes = confirmedUploadSizeForTest(t, blockID)
	confirmed.BlockObject.Key = "cell-a/shards/0000000000000007/block.blk"
	confirmed.BlockObject.SizeBytes = confirmed.SealedSizeBytes
	if err := idx.PutConfirmedUpload(confirmed); err != nil {
		t.Fatalf("PutConfirmedUpload block %d: %v", blockID, err)
	}
}

func nextConfirmedUpload(t *testing.T, iter index.ConfirmedUploadIterator, label string) index.ConfirmedUpload {
	t.Helper()

	upload, err := iter.Next()
	if err != nil {
		t.Fatalf("Next %s: %v", label, err)
	}
	return upload
}

func assertConfirmedUpload(t *testing.T, upload index.ConfirmedUpload, blockID uint64) {
	t.Helper()

	if upload.BlockID != blockID {
		t.Fatalf("BlockID = %d, want %d", upload.BlockID, blockID)
	}
	wantSize := confirmedUploadSizeForTest(t, blockID)
	if upload.SealedSizeBytes != wantSize {
		t.Fatalf("SealedSizeBytes = %d, want %d", upload.SealedSizeBytes, wantSize)
	}
}

func confirmedUploadOffsetForTest(t *testing.T, blockID uint64) int64 {
	t.Helper()

	switch blockID {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		t.Fatalf("unexpected test Block ID %d", blockID)
		return 0
	}
}

func confirmedUploadSizeForTest(t *testing.T, blockID uint64) int64 {
	t.Helper()

	switch blockID {
	case 1:
		return 1024
	case 2:
		return 2048
	case 3:
		return 3072
	default:
		t.Fatalf("unexpected test Block ID %d", blockID)
		return 0
	}
}
