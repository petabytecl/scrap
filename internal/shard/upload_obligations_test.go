package shard

import (
	"testing"
	"time"
)

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

	now := time.Now()
	pendingRetry := obligations.beginRetry(now, now.Add(time.Minute))
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

	now := time.Now()
	retryAfter := now.Add(time.Minute)
	first := obligations.beginRetry(now, retryAfter)
	if len(first) != 1 {
		t.Fatalf("first retry batch = %d, want 1", len(first))
	}
	second := obligations.beginRetry(now, retryAfter)
	if len(second) != 0 {
		t.Fatalf("second retry batch = %d, want 0 while seal is in flight", len(second))
	}

	stats := obligations.pressureStats(nil)
	if stats.pendingBlocks != 1 || stats.pendingBytes != 10 {
		t.Fatalf("pressure stats while in flight = %+v, want one local obligation", stats)
	}
}

func TestUploadObligationsRetriesTimedOutInFlightSeal(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})

	now := time.Now()
	retryAfter := now.Add(time.Minute)
	if got := len(obligations.beginRetry(now, retryAfter)); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}

	if got := len(obligations.beginRetry(retryAfter.Add(-time.Nanosecond), retryAfter.Add(time.Minute))); got != 0 {
		t.Fatalf("retry batch before in-flight timeout = %d, want 0", got)
	}
	if got := len(obligations.beginRetry(retryAfter, retryAfter.Add(time.Minute))); got != 1 {
		t.Fatalf("retry batch after in-flight timeout = %d, want 1", got)
	}
}

func TestUploadObligationsRetriesFailedInFlightSeal(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})

	now := time.Now()
	if got := len(obligations.beginRetry(now, now.Add(time.Minute))); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}
	obligations.markRetryFailed(1, now)

	retry := obligations.beginRetry(now, now.Add(time.Minute))
	if len(retry) != 1 {
		t.Fatalf("retry batch after failed proposal = %d, want 1", len(retry))
	}
}

func TestUploadObligationsDelaysFailedSealRetry(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})

	now := time.Now()
	if got := len(obligations.beginRetry(now, now.Add(time.Minute))); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}
	retryAfter := now.Add(time.Minute)
	obligations.markRetryFailed(1, retryAfter)

	if got := len(obligations.beginRetry(now, retryAfter.Add(time.Minute))); got != 0 {
		t.Fatalf("retry batch before retry_after = %d, want 0", got)
	}
	if got := len(obligations.beginRetry(retryAfter, retryAfter.Add(time.Minute))); got != 1 {
		t.Fatalf("retry batch at retry_after = %d, want 1", got)
	}
}

func TestUploadObligationsIgnoresLateFailureAfterForget(t *testing.T) {
	var obligations uploadObligations
	obligations.recordLocal(PendingUpload{BlockID: 1, ShardID: uploadObligationsTestShardID, SealedSizeBytes: 10})
	now := time.Now()
	if got := len(obligations.beginRetry(now, now.Add(time.Minute))); got != 1 {
		t.Fatalf("first retry batch = %d, want 1", got)
	}

	obligations.forget(1)
	obligations.markRetryFailed(1, now)

	if got := len(obligations.beginRetry(now, now.Add(time.Minute))); got != 0 {
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

// #471: markSealRetryFailed and the outbox wrapper were at 0% coverage. The
// retry-failed path re-arms the obligation so a failed seal retry is retried
// after the backoff instead of staying in-flight forever.
func TestUploadObligationsSealRetryFailureReArmsAfterBackoff(t *testing.T) {
	lifecycle := newBlockUploadLifecycle()
	outbox := newUploadOutbox("", nil, lifecycle)
	lifecycle.recordLocalSeal(PendingUpload{
		BlockID:         1,
		ShardID:         uploadObligationsTestShardID,
		SealedSizeBytes: 1,
	})

	now := time.Now()
	backoff := now.Add(time.Minute)
	if pending := outbox.BeginBlockSealRetry(now, backoff); len(pending) != 1 {
		t.Fatalf("initial retry batch = %d, want 1", len(pending))
	}
	// In-flight obligations are not handed out again.
	if pending := outbox.BeginBlockSealRetry(now, backoff); len(pending) != 0 {
		t.Fatalf("retry batch while in flight = %d, want 0", len(pending))
	}

	outbox.MarkBlockSealRetryFailed(1, backoff)

	// Before the backoff expires the failed obligation stays parked.
	if pending := outbox.BeginBlockSealRetry(now, backoff); len(pending) != 0 {
		t.Fatalf("retry batch before backoff = %d, want 0", len(pending))
	}
	// After the backoff it is handed out again.
	after := backoff.Add(time.Second)
	pending := outbox.BeginBlockSealRetry(after, after.Add(time.Minute))
	if len(pending) != 1 || pending[0].BlockID != 1 {
		t.Fatalf("retry batch after backoff = %+v, want Block 1", pending)
	}
	if outbox.UploadObligationCount() != 1 {
		t.Fatalf("obligation count = %d, want 1", outbox.UploadObligationCount())
	}
}
