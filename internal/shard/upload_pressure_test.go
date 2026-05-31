package shard_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestUploadPressureRejectsWritesAndResumesAfterDrain(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled: true,
		Pressure: shard.UploadPressureConfig{
			BudgetBytes: 100,
			WarnPct:     0.80,
			PressurePct: 0.90,
			CriticalPct: 0.95,
		},
	})
	ctx := context.Background()

	writeUploadPressureDoc(t, s, "tx-pressure-1", bytes.Repeat([]byte("a"), 64))
	writeUploadPressureDoc(t, s, "tx-pressure-2", []byte("b"))
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelCritical)

	_, err := s.WriteDocument(ctx, "tx-pressure-3", "doc.bin", "application/octet-stream", "", bytes.NewReader([]byte("rejected")))
	if err == nil {
		t.Fatal("expected write rejection under upload pressure")
	}
	if !errors.Is(err, storeapi.ErrResourceExhausted) {
		t.Fatalf("expected resource exhausted rejection, got %v", err)
	}
	reason, ok := storeapi.ResourceExhaustedReason(err)
	if !ok || reason != storeapi.ResourceExhaustedReasonUploadPressure {
		t.Fatalf("resource exhausted reason = %q, %v; want upload_pressure", reason, ok)
	}

	if err := s.ConfirmUploadForTest(ctx, 1, "cell-a/shards/0000000000000007/0000000000000001", "etag-1"); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelOK)

	writeUploadPressureDoc(t, s, "tx-pressure-4", []byte("accepted"))
}

func TestOrphanedSealTriggersUploadPressureAdmissionBeforeOutboxCommit(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled: true,
		Pressure: shard.UploadPressureConfig{
			BudgetBytes: 40,
			WarnPct:     0.80,
			PressurePct: 0.90,
			CriticalPct: 0.95,
		},
	})
	ctx := context.Background()

	s.AddOrphanedSealForTest(shard.PendingUpload{
		BlockID:         99,
		ShardID:         testShardID,
		SealedSizeBytes: 41,
		SealedAtUs:      time.Now().UnixMicro(),
	})
	uploads, err := s.PendingUploadsForTest()
	if err != nil {
		t.Fatalf("PendingUploadsForTest: %v", err)
	}
	if len(uploads) != 0 {
		t.Fatalf("committed pending uploads = %d, want 0", len(uploads))
	}
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelCritical)

	snapshot := s.UploadPressureForTest()
	if snapshot.PendingBlocks != 1 {
		t.Fatalf("pending blocks = %d, want 1 orphaned sealed Block", snapshot.PendingBlocks)
	}
	if snapshot.PendingBytes <= 0 {
		t.Fatalf("pending bytes = %d, want orphaned sealed Block bytes", snapshot.PendingBytes)
	}

	_, err = s.WriteDocument(ctx, "tx-orphan-pressure-1", "doc.bin", "application/octet-stream", "", bytes.NewReader([]byte("rejected")))
	if err == nil {
		t.Fatal("expected write rejection from orphaned upload pressure")
	}
	reason, ok := storeapi.ResourceExhaustedReason(err)
	if !ok || reason != storeapi.ResourceExhaustedReasonUploadPressure {
		t.Fatalf("resource exhausted reason = %q, %v; want upload_pressure", reason, ok)
	}
}

func TestUploadPressureWarnRaisesConcurrencyAndClears(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Concurrency: 2,
		Pressure: shard.UploadPressureConfig{
			BudgetBytes: 200,
			WarnPct:     0.20,
			PressurePct: 0.90,
			CriticalPct: 0.95,
		},
	})
	ctx := context.Background()

	writeUploadPressureDoc(t, s, "tx-warn-1", bytes.Repeat([]byte("a"), 64))
	writeUploadPressureDoc(t, s, "tx-warn-2", []byte("b"))
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelWarn)

	if got := s.UploadConcurrencyForTest(); got != 4 {
		t.Fatalf("warn concurrency = %d, want 4", got)
	}

	if err := s.ConfirmUploadForTest(ctx, 1, "cell-a/shards/0000000000000007/0000000000000001", "etag-1"); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelOK)
	if got := s.UploadConcurrencyForTest(); got != 2 {
		t.Fatalf("cleared concurrency = %d, want 2", got)
	}
}

func TestUploadPressureCriticalPausesDeepScrubAndResumes(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled: true,
		Pressure: shard.UploadPressureConfig{
			BudgetBytes: 100,
			WarnPct:     0.80,
			PressurePct: 0.90,
			CriticalPct: 0.95,
		},
	})
	ctx := context.Background()

	writeUploadPressureDoc(t, s, "tx-critical-1", bytes.Repeat([]byte("a"), 64))
	writeUploadPressureDoc(t, s, "tx-critical-2", []byte("b"))
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelCritical)
	if !s.DeepScrubPausedForTest() {
		t.Fatal("expected deep scrub pause gate to be paused at critical upload pressure")
	}

	if err := s.ConfirmUploadForTest(ctx, 1, "cell-a/shards/0000000000000007/0000000000000001", "etag-1"); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitUploadPressureLevel(t, s, shard.UploadPressureLevelOK)
	if s.DeepScrubPausedForTest() {
		t.Fatal("expected deep scrub pause gate to resume after pressure clears")
	}
}

func TestUploadOTelMetricsRegistersAndEmits(t *testing.T) {
	provider, reader := newTestMeter(t)
	metrics, err := shard.NewUploadOTelMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("new upload otel metrics: %v", err)
	}

	metrics.SetPending(7, 123, 2)
	metrics.RecordUpload(7, "success", time.Second)
	metrics.RecordVerify(7, "pass")
	metrics.SetPressureLevel(7, shard.UploadPressureLevelWarn)
	metrics.SetConcurrency(7, 4)
	metrics.SetAuthPaused(7, true)

	rm := collectMetrics(t, reader)
	for _, name := range []string{
		"scrap.upload.pending_bytes",
		"scrap.upload.pending_blocks",
		"scrap.upload.total",
		"scrap.upload.duration",
		"scrap.upload.verify_total",
		"scrap.upload.pressure_level",
		"scrap.upload.concurrency",
		"scrap.upload.auth_paused",
	} {
		if findMetric(rm, name) == nil {
			t.Fatalf("metric %s not found", name)
		}
	}
}

func TestParseUploadPressureConfigFromEnv(t *testing.T) {
	t.Setenv("SCRAP_UPLOAD_BUDGET", "2048")
	t.Setenv("SCRAP_UPLOAD_WARN_PCT", "0.70")
	t.Setenv("SCRAP_UPLOAD_PRESSURE_PCT", "0.85")
	t.Setenv("SCRAP_UPLOAD_CRITICAL_PCT", "0.95")

	cfg := shard.ParseUploadPressureConfigFromEnv()
	if cfg.BudgetBytes != 2048 {
		t.Fatalf("BudgetBytes = %d, want 2048", cfg.BudgetBytes)
	}
	if cfg.WarnPct != 0.70 || cfg.PressurePct != 0.85 || cfg.CriticalPct != 0.95 {
		t.Fatalf("thresholds = %.2f/%.2f/%.2f, want 0.70/0.85/0.95", cfg.WarnPct, cfg.PressurePct, cfg.CriticalPct)
	}
}

func TestParseUploadPressureConfigFromEnv_WholePercentagesResetToDefaults(t *testing.T) {
	t.Setenv("SCRAP_UPLOAD_BUDGET", "2048")
	t.Setenv("SCRAP_UPLOAD_WARN_PCT", "70")
	t.Setenv("SCRAP_UPLOAD_PRESSURE_PCT", "85")
	t.Setenv("SCRAP_UPLOAD_CRITICAL_PCT", "95")

	cfg := shard.ParseUploadPressureConfigFromEnv()
	if cfg.WarnPct != shard.DefaultUploadWarnPct || cfg.PressurePct != shard.DefaultUploadPressurePct || cfg.CriticalPct != shard.DefaultUploadCriticalPct {
		t.Fatalf("thresholds = %.2f/%.2f/%.2f, want defaults %.2f/%.2f/%.2f",
			cfg.WarnPct, cfg.PressurePct, cfg.CriticalPct,
			shard.DefaultUploadWarnPct, shard.DefaultUploadPressurePct, shard.DefaultUploadCriticalPct)
	}
}

func writeUploadPressureDoc(t *testing.T, s *shard.Shard, txID string, payload []byte) {
	t.Helper()

	if _, err := s.WriteDocument(context.Background(), txID, "doc.bin", "application/octet-stream", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument %s: %v", txID, err)
	}
}

func waitUploadPressureLevel(t *testing.T, s *shard.Shard, want shard.UploadPressureLevel) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := s.UploadPressureForTest()
		if snapshot.Level == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for upload pressure level %s; got %+v", want, s.UploadPressureForTest())
}
