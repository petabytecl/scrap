package shard_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/eviction"
	"github.com/petabytecl/scrap/internal/shard"
)

func TestShardCreateEvictionPlanStoresTokenAndSkipsLeaderBlocks(t *testing.T) {
	s := openEvictionPlanTestShard(t)

	writeConfirmedUploadForEvictionPlan(t, s)

	blockPath := block.FilePath(filepath.Join(s.DataDirForTest(), "blocks"), 1)
	assertBlockPathExists(t, blockPath, "before plan")

	plan := createLeaderEvictionPlan(t, s)
	assertLeaderEvictionPlan(t, plan)
	assertBlockPathExists(t, blockPath, "after dry-run plan")
	assertEvictionPlanStored(t, s, plan)
}

func writeConfirmedUploadForEvictionPlan(t *testing.T, s *shard.Shard) {
	t.Helper()

	ctx := context.Background()
	if _, err := s.WriteDocument(ctx, "tx-evict-plan-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-evict-plan-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	pending := waitPendingUploads(t, s, 1)[0]
	if err := s.ConfirmUploadForTest(ctx, confirmedUploadForTest(pending.SealedSizeBytes)); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitPendingUploads(t, s, 0)
}

func createLeaderEvictionPlan(t *testing.T, s *shard.Shard) eviction.Plan {
	t.Helper()

	ctx := context.Background()
	maxBlocks := 1
	maxBytes := int64(4096)
	plan, err := s.CreateEvictionPlan(ctx, eviction.PlanRequest{
		MemberHostname: "scrapd-1",
		MaxBlocks:      &maxBlocks,
		MaxBytes:       &maxBytes,
		Reason:         eviction.ReasonEvidenceRun,
	})
	if err != nil {
		t.Fatalf("CreateEvictionPlan: %v", err)
	}
	return plan
}

func assertLeaderEvictionPlan(t *testing.T, plan eviction.Plan) {
	t.Helper()

	assertLeaderPlanIdentity(t, plan)
	assertLeaderPlanBounds(t, plan)
	assertLeaderPlanSkipEvidence(t, plan)
}

func assertLeaderPlanIdentity(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if plan.PlanID == "" {
		t.Fatal("PlanID is empty")
	}
	if plan.MemberHostname != "scrapd-1" || plan.MemberID != "member-a" {
		t.Fatalf("plan identity = %s/%s, want scrapd-1/member-a", plan.MemberHostname, plan.MemberID)
	}
	if plan.GeneratedAtUs <= 0 || plan.ExpiresAtUs <= plan.GeneratedAtUs {
		t.Fatalf("plan times invalid: generated=%d expires=%d", plan.GeneratedAtUs, plan.ExpiresAtUs)
	}
}

func assertLeaderPlanBounds(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if plan.EffectiveBounds.MaxBlocks != 1 || plan.EffectiveBounds.MaxBytes != 4096 {
		t.Fatalf("effective bounds = %+v, want max_blocks=1 max_bytes=4096", plan.EffectiveBounds)
	}
}

func assertLeaderPlanSkipEvidence(t *testing.T, plan eviction.Plan) {
	t.Helper()

	if len(plan.Selected) != 0 {
		t.Fatalf("selected Blocks = %+v, want none on leader", plan.Selected)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != eviction.SkipReasonLeaderHotCopyRequired {
		t.Fatalf("skipped = %+v, want leader_hot_copy_required", plan.Skipped)
	}
	if plan.Skipped[0].ConfirmedAtUs == 0 || plan.Skipped[0].EligibleAtUs == 0 {
		t.Fatalf("skipped evidence missing confirmed/eligible times: %+v", plan.Skipped[0])
	}
}

func assertEvictionPlanStored(t *testing.T, s *shard.Shard, plan eviction.Plan) {
	t.Helper()
	stored, ok := s.EvictionPlanForTest(plan.PlanID)
	if !ok {
		t.Fatal("stored plan token not found")
	}
	if stored.PlanID != plan.PlanID {
		t.Fatalf("stored plan ID = %s, want %s", stored.PlanID, plan.PlanID)
	}
}

func assertBlockPathExists(t *testing.T, blockPath, label string) {
	t.Helper()

	if _, err := os.Stat(blockPath); err != nil {
		t.Fatalf("stat Block %s: %v", label, err)
	}
}

func openEvictionPlanTestShard(t *testing.T) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:        t.TempDir(),
		ShardID:        testShardID,
		RaftID:         1,
		Peers:          map[uint64]string{1: "localhost:9091"},
		BlockSealSize:  41,
		TickInterval:   10 * time.Millisecond,
		BootstrapGrace: time.Second,
		Upload:         shard.UploadConfig{Enabled: true},
		Eviction: shard.EvictionConfig{
			HotResidencyWindow:   0,
			PlanTTL:              time.Minute,
			RecommendedMaxBlocks: 10,
			RecommendedMaxBytes:  640 << 20,
			MaxValidateSamples:   1,
		},
		MemberHostname: "scrapd-1",
		MemberID:       "member-a",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shard did not become leader")
	return nil
}
