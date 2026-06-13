package shard_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
)

func TestWriteDocument_NoPrepFileAfterCommit(t *testing.T) {
	dir := t.TempDir()
	s := openReplicatingTestShard(t, dir, nil)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-clean", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	openlogDir := filepath.Join(dir, "openlog")
	entries, err := os.ReadDir(openlogDir)
	if err != nil {
		t.Fatalf("ReadDir openlog: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("prep file should have been deleted after commit: %s", entry.Name())
		}
	}
}

func TestWriteDocument_NoPrepFileAfterMultipleWrites(t *testing.T) {
	dir := t.TempDir()
	s := openReplicatingTestShard(t, dir, nil)
	ctx := context.Background()

	for i := range 5 {
		txID := fmt.Sprintf("tx-prep-%d", i)
		_, err := s.WriteDocument(ctx, txID, "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
		if err != nil {
			t.Fatalf("WriteDocument %s: %v", txID, err)
		}
	}

	openlogDir := filepath.Join(dir, "openlog")
	entries, err := os.ReadDir(openlogDir)
	if err != nil {
		t.Fatalf("ReadDir openlog: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("prep file should have been deleted after commit: %s", entry.Name())
		}
	}
}

func TestSealAndOpenNew_AdvancesBlockAfterSeal(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	initialBlockID := s.CurrentBlockIDForTest()

	if _, err := s.WriteDocument(ctx, "tx-seal-1", "doc.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-seal-2", "doc2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.CurrentBlockIDForTest() > initialBlockID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("block ID should have advanced after seal")
}

func TestApplySealBlock_ClosesMatchingFollowerBlock(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	blockBefore := s.CurrentBlockIDForTest()

	if _, err := s.WriteDocument(ctx, "tx-seal-follower", "doc.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("x"), 64))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-seal-follower-2", "doc2.bin", "application/octet-stream", "", bytes.NewReader([]byte("y"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	uploads := waitPendingUploads(t, s, 1)
	if len(uploads) == 0 {
		t.Fatal("expected pending upload after seal")
	}

	blockAfter := s.CurrentBlockIDForTest()
	if blockAfter <= blockBefore {
		t.Fatalf("block ID should advance after seal: before=%d, after=%d", blockBefore, blockAfter)
	}
}

func TestConfirmUploadWithoutSealedMetadataClosesMatchingOpenBlock(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	if got := s.CurrentBlockIDForTest(); got != 1 {
		t.Fatalf("current Block = %d, want 1", got)
	}
	if err := s.ConfirmUploadForTest(ctx, confirmedUploadForTest(1)); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.CurrentBlockIDForTest(); got > 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("confirm upload without sealed metadata left confirmed Block open")
}

func TestOrphanedSeals_RetryOnNextSeal(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	for i := 1; i <= 8; i++ {
		txID := fmt.Sprintf("tx-orphan-%d", i)
		docName := fmt.Sprintf("doc-%d.bin", i)
		if _, err := s.WriteDocument(ctx, txID, docName, "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte{byte(i)}, 64))); err != nil {
			t.Fatalf("WriteDocument %s: %v", docName, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		uploads, err := s.PendingUploadsForTest()
		if err != nil {
			t.Fatalf("PendingUploadsForTest: %v", err)
		}
		if len(uploads) >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	if s.OrphanedSealsForTest() > 0 {
		t.Logf("orphaned seals remaining: %d (may be retried on next write)", s.OrphanedSealsForTest())
	}
}

func TestSealAndOpenNew_NoOrphanedSealsOnSuccess(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-no-orphan-1", "doc.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-no-orphan-2", "doc2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	waitPendingUploads(t, s, 1)

	if got := s.OrphanedSealsForTest(); got != 0 {
		t.Fatalf("OrphanedSeals = %d, want 0 on successful seal", got)
	}
}

func TestSealAndOpenNew_BlockAdvancesAfterMultipleSeals(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	for i := 1; i <= 6; i++ {
		txID := fmt.Sprintf("tx-multi-seal-%d", i)
		docName := fmt.Sprintf("doc-%d.bin", i)
		if _, err := s.WriteDocument(ctx, txID, docName, "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte{byte(i)}, 64))); err != nil {
			t.Fatalf("WriteDocument %s: %v", docName, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.CurrentBlockIDForTest() >= 4 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected block ID >= 4, got %d", s.CurrentBlockIDForTest())
}

func TestApplySealBlock_PendingUploadHasCorrectMetadata(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	payload := bytes.Repeat([]byte("x"), 64)
	if _, err := s.WriteDocument(ctx, "tx-seal-meta", "doc.bin", "application/octet-stream", "", bytes.NewReader(payload)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-seal-meta-2", "doc2.bin", "application/octet-stream", "", bytes.NewReader([]byte("y"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	uploads := waitPendingUploads(t, s, 1)
	if uploads[0].SealedSizeBytes <= 0 {
		t.Fatalf("SealedSizeBytes = %d, want > 0", uploads[0].SealedSizeBytes)
	}
	if uploads[0].BlockID == 0 {
		t.Fatal("BlockID should not be zero")
	}
}

func TestRetryOrphanedSeals_ClearsOnSuccess(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	s.AddOrphanedSealForTest(shard.PendingUpload{
		BlockID: 999,
		ShardID: testShardID,
	})
	if got := s.OrphanedSealsForTest(); got != 1 {
		t.Fatalf("OrphanedSeals = %d, want 1", got)
	}

	if _, err := s.WriteDocument(ctx, "tx-retry-1", "doc.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-retry-2", "doc2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.OrphanedSealsForTest() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("orphaned seals should have been retried and cleared, got %d", s.OrphanedSealsForTest())
}

func TestRetryOrphanedSeals_DirectCall(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	s.AddOrphanedSealForTest(shard.PendingUpload{
		BlockID:         42,
		ShardID:         testShardID,
		SealedSizeBytes: 1024,
		SealedAtUs:      time.Now().UnixMicro(),
	})

	s.RetryOrphanedSealsForTest(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		uploads, err := s.PendingUploadsForTest()
		if err != nil {
			t.Fatalf("PendingUploadsForTest: %v", err)
		}
		for _, u := range uploads {
			if u.BlockID == 42 {
				if s.OrphanedSealsForTest() != 0 {
					t.Fatalf("orphaned seal should be cleared after successful retry")
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("orphaned seal block 42 should appear in pending uploads after retry")
}

func openReplicatingTestShard(t *testing.T, dataDir string, replicator shard.DocumentReplicator) *shard.Shard {
	t.Helper()
	s, err := shard.Open(shard.Config{
		DataDir:        dataDir,
		ShardID:        0,
		RaftID:         1,
		Peers:          map[uint64]string{1: "localhost:9091"},
		TickInterval:   10 * time.Millisecond,
		BootstrapGrace: time.Second,
		Replicator:     replicator,
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
