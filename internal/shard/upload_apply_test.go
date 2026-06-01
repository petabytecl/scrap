package shard

import (
	"errors"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

func TestApplyConfirmUploadFailsClosedWithoutSealedMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)

	s := shardForApplyTest(idx)
	err := s.applyConfirmUpload(confirmUploadCommandForApplyTest(42))
	if !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("applyConfirmUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	if _, err := idx.GetConfirmedUpload(42); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestApplyConfirmUploadRejectsBlockSizeMismatch(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         42,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	confirm := confirmUploadCommandForApplyTest(42)
	confirm.BlockObject.SizeBytes--
	err := shardForApplyTest(idx).applyConfirmUpload(confirm)
	if err == nil {
		t.Fatal("applyConfirmUpload succeeded with mismatched Block size")
	}
	if _, err := idx.GetConfirmedUpload(42); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
	if _, err := idx.GetPendingUpload(42); err != nil {
		t.Fatalf("GetPendingUpload after rejected confirm: %v", err)
	}
}

func TestApplyConfirmUploadUpdatesDuplicateConfirmationMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)
	existing := confirmedUploadForApplyTest(42, 1716700001000000, shardApplyValidationValue("old-block"), shardApplyValidationValue("old-index"))
	if err := idx.PutConfirmedUpload(existing); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	confirm := confirmUploadCommandForApplyTest(42)
	confirm.ConfirmedAtUs = 1716700002000000
	confirm.BlockObject.ValidationToken = shardApplyValidationValue("new-block")
	confirm.IndexObject.ValidationToken = shardApplyValidationValue("new-index")

	if err := shardForApplyTest(idx).applyConfirmUpload(confirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	got, err := idx.GetConfirmedUpload(42)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	want := confirmedUploadForApplyTest(42, confirm.GetConfirmedAtUs(), shardApplyValidationValue("new-block"), shardApplyValidationValue("new-index"))
	if got != want {
		t.Fatalf("confirmed upload = %+v, want %+v", got, want)
	}
}

func openApplyTestIndex(t *testing.T) *index.Index {
	t.Helper()

	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func shardForApplyTest(idx *index.Index) *Shard {
	return &Shard{
		idx:     idx,
		uploads: newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}
}

func confirmedUploadForApplyTest(blockID uint64, confirmedAtUs int64, blockValidation, indexValidation string) index.ConfirmedUpload {
	confirm := confirmUploadCommandForApplyTest(blockID)
	return index.ConfirmedUpload{
		BlockID:         blockID,
		ShardID:         confirm.GetShardId(),
		ConfirmedAtUs:   confirmedAtUs,
		SealedSizeBytes: confirm.GetBlockObject().GetSizeBytes(),
		BlockObject: index.BackendObjectMetadata{
			Key:             confirm.GetBlockObject().GetKey(),
			SizeBytes:       confirm.GetBlockObject().GetSizeBytes(),
			ValidationToken: blockValidation,
		},
		IndexObject: index.BackendObjectMetadata{
			Key:             confirm.GetIndexObject().GetKey(),
			SizeBytes:       confirm.GetIndexObject().GetSizeBytes(),
			ValidationToken: indexValidation,
		},
	}
}

func confirmUploadCommandForApplyTest(blockID uint64) *scrapv1.ConfirmUpload {
	prefix := "cell-a/shards/0000000000000007/000000000000002a"
	return &scrapv1.ConfirmUpload{
		BlockId: blockID,
		ShardId: 7,
		BlockObject: &scrapv1.BackendObjectMetadata{
			Key:             prefix + ".blk",
			SizeBytes:       67108864,
			ValidationToken: shardApplyValidationValue("block"),
		},
		IndexObject: &scrapv1.BackendObjectMetadata{
			Key:             prefix + ".idx",
			SizeBytes:       4096,
			ValidationToken: shardApplyValidationValue("index"),
		},
		ConfirmedAtUs: 1716700001000000,
	}
}

func shardApplyValidationValue(kind string) string {
	return kind + "-validation"
}
