package shard

import "testing"

const uploadObligationsTestShardID uint64 = 7

func TestUploadObligationsRetainsAllLocalSealsForRetryAndPressure(t *testing.T) {
	var obligations uploadObligations
	const total = 70

	for i := 1; i <= total; i++ {
		obligations.recordLocal(PendingUpload{
			BlockID:         uint64(i),
			ShardID:         uploadObligationsTestShardID,
			SealedSizeBytes: 1,
		})
	}

	pendingRetry := obligations.pendingRetry()
	if len(pendingRetry) != total {
		t.Fatalf("pending retry seals = %d, want %d", len(pendingRetry), total)
	}
	for i, upload := range pendingRetry {
		wantBlockID := uint64(i + 1)
		if upload.BlockID != wantBlockID {
			t.Fatalf("pending retry block at %d = %d, want %d", i, upload.BlockID, wantBlockID)
		}
	}

	stats := obligations.pressureStats(nil)
	if stats.pendingBlocks != total {
		t.Fatalf("pressure pending blocks = %d, want %d", stats.pendingBlocks, total)
	}
	if stats.pendingBytes != total {
		t.Fatalf("pressure pending bytes = %d, want %d", stats.pendingBytes, total)
	}
}

func TestUploadObligationsPressureStatsDeduplicatesCommittedSeal(t *testing.T) {
	var obligations uploadObligations

	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})
	obligations.recordLocal(PendingUpload{BlockID: 2, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 20})

	stats := obligations.pressureStats([]PendingUpload{
		{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 100},
	})

	if stats.pendingBlocks != 2 {
		t.Fatalf("pressure pending blocks = %d, want 2", stats.pendingBlocks)
	}
	if stats.pendingBytes != 120 {
		t.Fatalf("pressure pending bytes = %d, want 120", stats.pendingBytes)
	}
}
