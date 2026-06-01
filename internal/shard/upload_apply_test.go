package shard

import (
	"errors"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

func TestApplyConfirmUploadFailsClosedWithoutSealedMetadata(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	s := &Shard{idx: idx}
	err = s.applyConfirmUpload(confirmUploadCommandForApplyTest(42))
	if !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("applyConfirmUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	if _, err := idx.GetConfirmedUpload(42); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
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
