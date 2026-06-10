package shard

import (
	"context"
	"errors"
	"testing"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/index"
)

const uploadApplyTestBlockID = 42

func TestApplyConfirmUploadSkipsCatalogWithoutSealedMetadata(t *testing.T) {
	idx := openApplyTestIndex(t)

	s := shardForApplyTest(t, idx)
	err := s.applyConfirmUpload(confirmUploadCommandForApplyTest())
	if err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}

	health, err := s.EvictionHealthSnapshot(context.Background())
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if health.Pressure != eviction.HealthPressureDegraded || health.MetadataLossBlocks != 1 {
		t.Fatalf("eviction health = %+v, want degraded metadata loss", health)
	}
}

func TestApplyConfirmUploadSkipsCatalogAndClearsPendingOnBlockSizeMismatch(t *testing.T) {
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
	s := shardForApplyTest(t, idx)
	s.blockUploadLifecycleLocked().recordLocalSeal(index.PendingUpload{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	})
	err := s.applyConfirmUpload(confirm)
	if err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
	if _, err := idx.GetPendingUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	if got := s.blockUploadLifecycleLocked().obligationCount(); got != 0 {
		t.Fatalf("upload obligations = %d, want 0", got)
	}

	health, err := s.EvictionHealthSnapshot(context.Background())
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if health.Pressure != eviction.HealthPressureDegraded || health.MetadataLossBlocks != 1 {
		t.Fatalf("eviction health = %+v, want degraded metadata loss", health)
	}
}

func TestApplyConfirmUploadIgnoresStaleGenerationAndKeepsPending(t *testing.T) {
	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:          uploadApplyTestBlockID,
		ShardID:          7,
		SealedSizeBytes:  67108864,
		SealedAtUs:       1716700000000000,
		UploadGeneration: 1716700002000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	staleConfirm := confirmUploadCommandForApplyTest()
	staleConfirm.UploadGeneration = 1716700001000000
	s := shardForApplyTest(t, idx)
	if err := s.applyConfirmUpload(staleConfirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}

	pending, err := idx.GetPendingUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending.UploadGeneration != 1716700002000000 {
		t.Fatalf("pending upload generation = %d, want replacement generation", pending.UploadGeneration)
	}
	if _, err := idx.GetConfirmedUpload(uploadApplyTestBlockID); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
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

func TestApplyConfirmUploadIgnoresStaleDuplicateGeneration(t *testing.T) {
	idx := openApplyTestIndex(t)
	existing := confirmedUploadForApplyTest(1716700002000000, shardApplyValidationValue("new-block"), shardApplyValidationValue("new-index"))
	existing.UploadGeneration = 1716700002000000
	if err := idx.PutConfirmedUpload(existing); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	staleConfirm := confirmUploadCommandForApplyTest()
	staleConfirm.ConfirmedAtUs = 1716700003000000
	staleConfirm.UploadGeneration = 1716700001000000
	staleConfirm.BlockObject.ValidationToken = shardApplyValidationValue("stale-block")
	staleConfirm.IndexObject.ValidationToken = shardApplyValidationValue("stale-index")

	if err := shardForApplyTest(t, idx).applyConfirmUpload(staleConfirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	got, err := idx.GetConfirmedUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != existing {
		t.Fatalf("confirmed upload = %+v, want existing %+v", got, existing)
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
