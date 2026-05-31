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

	pendingRetry := obligations.beginRetry()
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

func TestUploadObligationsDoesNotRetryInFlightSeal(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})

	first := obligations.beginRetry()
	if len(first) != 1 {
		t.Fatalf("first retry batch = %d, want 1", len(first))
	}
	second := obligations.beginRetry()
	if len(second) != 0 {
		t.Fatalf("second retry batch = %d, want 0 while seal is in flight", len(second))
	}

	stats := obligations.pressureStats(nil)
	if stats.pendingBlocks != 1 || stats.pendingBytes != 10 {
		t.Fatalf("pressure stats while in flight = %+v, want one local obligation", stats)
	}
}

func TestUploadObligationsRetriesFailedInFlightSeal(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})

	if got := len(obligations.beginRetry()); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}
	obligations.markRetryFailed(1)

	retry := obligations.beginRetry()
	if len(retry) != 1 {
		t.Fatalf("retry batch after failed proposal = %d, want 1", len(retry))
	}
}

func TestUploadObligationsIgnoresLateFailureAfterForget(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})
	if got := len(obligations.beginRetry()); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}

	obligations.forget(1)
	obligations.markRetryFailed(1)

	if got := len(obligations.beginRetry()); got != 0 {
		t.Fatalf("retry batch after forgotten obligation failed late = %d, want 0", got)
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
