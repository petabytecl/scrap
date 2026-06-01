package shard

import (
	"errors"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/index"
)

const uploadApplyTestBlockID = 42

func TestApplyConfirmUploadFailsClosedWithoutSealedMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)

	s := shardForApplyTest(t, idx)
	err := s.applyConfirmUpload(confirmUploadCommandForApplyTest())
	if !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("applyConfirmUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestApplyConfirmUploadRejectsBlockSizeMismatch(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	confirm := confirmUploadCommandForApplyTest()
	confirm.BlockObject.SizeBytes--
	err := shardForApplyTest(t, idx).applyConfirmUpload(confirm)
	if err == nil {
		t.Fatal("applyConfirmUpload succeeded with mismatched Block size")
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
	if _, err := idx.GetPendingUpload(uploadApplyTestBlockID); err != nil {
		t.Fatalf("GetPendingUpload after rejected confirm: %v", err)
	}
}

func TestApplyConfirmUploadUpdatesDuplicateConfirmationMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)
	existing := confirmedUploadForApplyTest(1716700001000000, shardApplyValidationValue("old-block"), shardApplyValidationValue("old-index"))
	if err := idx.PutConfirmedUpload(existing); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	confirm := confirmUploadCommandForApplyTest()
	confirm.ConfirmedAtUs = 1716700002000000
	confirm.BlockObject.ValidationToken = shardApplyValidationValue("new-block")
	confirm.IndexObject.ValidationToken = shardApplyValidationValue("new-index")

	if err := shardForApplyTest(t, idx).applyConfirmUpload(confirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	got, err := idx.GetConfirmedUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	want := confirmedUploadForApplyTest(confirm.GetConfirmedAtUs(), shardApplyValidationValue("new-block"), shardApplyValidationValue("new-index"))
	if got != want {
		t.Fatalf("confirmed upload = %+v, want %+v", got, want)
	}
}

func TestApplyConfirmUploadRecordsCommittedAuthorityForRebuild(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	s := shardForApplyTest(t, idx)
	confirm := confirmUploadCommandForApplyTest()
	if err := s.applyConfirmUpload(confirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	poisoned := confirmedUploadForApplyTest(1716700003000000, shardApplyValidationValue("poisoned-block"), shardApplyValidationValue("poisoned-index"))
	if err := idx.PutConfirmedUpload(poisoned); err != nil {
		t.Fatalf("poison confirmed upload: %v", err)
	}

	got, err := s.confirmedUploadForRebuild(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("confirmedUploadForRebuild: %v", err)
	}
	want := confirmedUploadForApplyTest(confirm.GetConfirmedAtUs(), shardApplyValidationValue("block"), shardApplyValidationValue("index"))
	if got != want {
		t.Fatalf("rebuild authority = %+v, want committed authority %+v", got, want)
	}
}

func TestConfirmedUploadForRebuildRequiresCommittedAuthority(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutConfirmedUpload(confirmedUploadForApplyTest(1716700001000000, shardApplyValidationValue("block"), shardApplyValidationValue("index"))); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	_, err := shardForApplyTest(t, idx).confirmedUploadForRebuild(uploadApplyTestBlockID)
	if !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("confirmedUploadForRebuild error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestConfirmedUploadForRebuildSurvivesRestart(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	s := shardForApplyTest(t, idx)
	confirm := confirmUploadCommandForApplyTest()
	if err := s.applyConfirmUpload(confirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}

	restarted := &Shard{blocksDir: s.blocksDir}
	got, err := restarted.confirmedUploadForRebuild(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("confirmedUploadForRebuild after restart: %v", err)
	}
	want := confirmedUploadForApplyTest(confirm.GetConfirmedAtUs(), shardApplyValidationValue("block"), shardApplyValidationValue("index"))
	if got != want {
		t.Fatalf("rebuild authority after restart = %+v, want %+v", got, want)
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

func shardForApplyTest(t *testing.T, idx *index.Index) *Shard {
	t.Helper()

	return &Shard{
		blocksDir: t.TempDir(),
		idx:       idx,
		uploads:   newUploadController(nil, UploadConfig{}, 7, nil, nil, nil),
	}
}

func confirmedUploadForApplyTest(confirmedAtUs int64, blockValidation, indexValidation string) index.ConfirmedUpload {
	confirm := confirmUploadCommandForApplyTest()
	return index.ConfirmedUpload{
		BlockID:         uploadApplyTestBlockID,
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

func confirmUploadCommandForApplyTest() *scrapv1.ConfirmUpload {
	prefix := "cell-a/shards/0000000000000007/000000000000002a"
	return &scrapv1.ConfirmUpload{
		BlockId: uploadApplyTestBlockID,
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
