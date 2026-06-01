package index_test

import (
	"errors"
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
