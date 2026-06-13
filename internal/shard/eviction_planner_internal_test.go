package shard

import (
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestEvictionCandidatesExcludePendingReplacementUploads(t *testing.T) {
	idx := openApplyTestIndex(t)
	confirmed := confirmedUploadForApplyTest(1716700001000000, shardApplyValidationValue("block"), shardApplyValidationValue("index"))
	if err := idx.PutConfirmedUpload(confirmed); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:          uploadApplyTestBlockID,
		ShardID:          7,
		SealedSizeBytes:  confirmed.SealedSizeBytes,
		SealedAtUs:       1716700002000000,
		UploadGeneration: 1716700002000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}

	candidates, err := shardForApplyTest(t, idx).evictionCandidatesLocked()
	if err != nil {
		t.Fatalf("evictionCandidatesLocked: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want pending replacement upload excluded", candidates)
	}
}
