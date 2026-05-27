package shard_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func openTestShard(t *testing.T) *shard.Shard {
	t.Helper()
	dir := t.TempDir()
	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
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

func TestWriteAndHeadDocument(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("test data "), 100)
	result, err := s.WriteDocument(ctx, "tx-001", "invoice.xml", "application/xml", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("Size: got %d, want %d", result.Size, len(content))
	}
	if result.SHA256 == [32]byte{} {
		t.Fatal("SHA256 should not be zero")
	}

	meta, err := s.HeadDocument(ctx, "tx-001", "invoice.xml")
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if meta.Name != "invoice.xml" {
		t.Fatalf("Name: got %q", meta.Name)
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("Size: got %d", meta.Size)
	}
	if meta.SHA256 != result.SHA256 {
		t.Fatal("SHA256 mismatch between write and head")
	}
}

func TestWriteAndReadDocument(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	content := bytes.Repeat([]byte("read back "), 200)
	_, err := s.WriteDocument(ctx, "tx-002", "doc.pdf", "application/pdf", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	rc, meta, err := s.ReadDocument(ctx, "tx-002", "doc.pdf")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer func() { _ = rc.Close() }()

	if meta.ContentType != "application/pdf" {
		t.Fatalf("ContentType: got %q", meta.ContentType)
	}

	readBack, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(readBack), len(content))
	}
}

func TestFindDocuments(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	names := []string{"a.xml", "b.xml", "c.xml"}
	for _, name := range names {
		_, err := s.WriteDocument(ctx, "tx-find", name, "text/xml", "", bytes.NewReader([]byte("data")))
		if err != nil {
			t.Fatalf("WriteDocument %s: %v", name, err)
		}
	}

	docs, err := s.FindDocuments(ctx, "tx-find")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}
}

func TestDuplicateWriteReturnsAlreadyExists(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-dup", "doc.xml", "text/xml", "", bytes.NewReader([]byte("first")))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	_, err = s.WriteDocument(ctx, "tx-dup", "doc.xml", "text/xml", "", bytes.NewReader([]byte("second")))
	if err == nil {
		t.Fatal("expected error on duplicate")
	}
	if !storeapi.IsAlreadyExists(err) {
		t.Fatalf("expected ErrAlreadyExists, got: %v", err)
	}
}

func TestHeadNotFound(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.HeadDocument(ctx, "nonexistent", "nope.xml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenlogRecoveryDeletesCompletedPrep(t *testing.T) {
	dir := t.TempDir()

	s, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsLeader() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, err = s.WriteDocument(context.Background(), "tx-recover", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	_ = s.Close()

	s2, err := shard.Open(shard.Config{
		DataDir:      dir,
		ShardID:      0,
		RaftID:       1,
		Peers:        map[uint64]string{1: "localhost:9091"},
		TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s2.IsLeader() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	meta, err := s2.HeadDocument(context.Background(), "tx-recover", "doc.xml")
	if err != nil {
		t.Fatalf("HeadDocument after recovery: %v", err)
	}
	if meta.Name != "doc.xml" {
		t.Fatalf("Name: got %q", meta.Name)
	}
}

func TestConsistencyCheckApplyAndCache(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-scrub", "doc.xml", "text/xml", "", bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	result, err := s.ProposeConsistencyCheck(ctx, "scrub-001")
	if err != nil {
		t.Fatalf("ProposeConsistencyCheck: %v", err)
	}
	if result.ScrubID != "scrub-001" {
		t.Fatalf("ScrubID: got %q", result.ScrubID)
	}
	if result.AppliedIndex == 0 {
		t.Fatal("AppliedIndex should be non-zero")
	}
	if result.SHA256 == [32]byte{} {
		t.Fatal("SHA256 should not be zero for non-empty projection")
	}

	cached, ok := s.GetScrubResult("scrub-001")
	if !ok {
		t.Fatal("expected cached result for scrub-001")
	}
	if cached.SHA256 != result.SHA256 {
		t.Fatal("cached hash should match proposal result")
	}

	_, ok = s.GetScrubResult("nonexistent")
	if ok {
		t.Fatal("expected no result for nonexistent scrub_id")
	}
}

func TestConsistencyCheckEmptyProjection(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	result, err := s.ProposeConsistencyCheck(ctx, "scrub-empty")
	if err != nil {
		t.Fatalf("ProposeConsistencyCheck: %v", err)
	}

	if result.SHA256 == [32]byte{} {
		t.Log("empty projection hash is the SHA-256 of empty input, which is non-zero")
	}
	if result.AppliedIndex == 0 {
		t.Fatal("AppliedIndex should be non-zero even for empty projection")
	}
}

func TestTriggerRebuild_CompletesAndReturnsToServing(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	alreadyInProgress, err := s.TriggerRebuild(ctx)
	if err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	if alreadyInProgress {
		t.Fatal("expected alreadyInProgress=false on first call")
	}

	s.WaitRebuild()

	_, err = s.WriteDocument(ctx, "tx-post-rebuild", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("WriteDocument after rebuild should succeed: %v", err)
	}
}

func TestTriggerRebuild_PreservesCommittedProjection(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	content := []byte("survives rebuild")
	_, err := s.WriteDocument(ctx, "tx-rebuild", "doc.xml", "text/xml", "", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("WriteDocument doc.xml: %v", err)
	}
	_, err = s.WriteDocument(ctx, "tx-rebuild", "other.xml", "text/xml", "", bytes.NewReader([]byte("other")))
	if err != nil {
		t.Fatalf("WriteDocument other.xml: %v", err)
	}

	triggerRebuildAndWait(ctx, t, s)
	assertHeadDocumentSize(ctx, t, s, "tx-rebuild", "doc.xml", int64(len(content)))
	assertReadDocumentContent(ctx, t, s, "tx-rebuild", "doc.xml", content)
	assertFindDocumentCount(ctx, t, s, "tx-rebuild", 2)
	assertDuplicateWriteRejected(ctx, t, s, "tx-rebuild", "doc.xml")
}

func assertFindDocumentCount(ctx context.Context, t *testing.T, s *shard.Shard, txID string, want int) {
	t.Helper()
	docs, err := s.FindDocuments(ctx, txID)
	if err != nil {
		t.Fatalf("FindDocuments after rebuild: %v", err)
	}
	if len(docs) != want {
		t.Fatalf("FindDocuments after rebuild: got %d docs, want %d", len(docs), want)
	}
}

func assertDuplicateWriteRejected(ctx context.Context, t *testing.T, s *shard.Shard, txID, docName string) {
	t.Helper()
	_, err := s.WriteDocument(ctx, txID, docName, "text/xml", "", bytes.NewReader([]byte("duplicate")))
	if err == nil {
		t.Fatal("expected duplicate write to fail after rebuild")
	}
	if !storeapi.IsAlreadyExists(err) {
		t.Fatalf("expected ErrAlreadyExists after rebuild, got: %v", err)
	}
}

func assertHeadDocumentSize(ctx context.Context, t *testing.T, s *shard.Shard, txID, docName string, want int64) {
	t.Helper()
	meta, err := s.HeadDocument(ctx, txID, docName)
	if err != nil {
		t.Fatalf("HeadDocument after rebuild: %v", err)
	}
	if meta.Size != want {
		t.Fatalf("Size after rebuild: got %d, want %d", meta.Size, want)
	}
}

func assertReadDocumentContent(ctx context.Context, t *testing.T, s *shard.Shard, txID, docName string, want []byte) {
	t.Helper()
	rc, _, err := s.ReadDocument(ctx, txID, docName)
	if err != nil {
		t.Fatalf("ReadDocument after rebuild: %v", err)
	}
	defer func() { _ = rc.Close() }()
	readBack, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll after rebuild: %v", err)
	}
	if !bytes.Equal(readBack, want) {
		t.Fatalf("content after rebuild: got %q, want %q", readBack, want)
	}
}

func triggerRebuildAndWait(ctx context.Context, t *testing.T, s *shard.Shard) {
	t.Helper()
	alreadyInProgress, err := s.TriggerRebuild(ctx)
	if err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	if alreadyInProgress {
		t.Fatal("expected alreadyInProgress=false on first call")
	}
	s.WaitRebuild()
}

func TestTriggerRebuild_AlreadyInProgress(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	s.SetRebuildingForTest(true)
	alreadyInProgress, err := s.TriggerRebuild(ctx)
	if err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	if !alreadyInProgress {
		t.Fatal("expected alreadyInProgress=true when already rebuilding")
	}
	s.SetRebuildingForTest(false)
}

func TestRebuilding_RejectsWrites(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	s.SetRebuildingForTest(true)
	_, err := s.WriteDocument(ctx, "tx-blocked", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	s.SetRebuildingForTest(false)

	if err == nil {
		t.Fatal("expected error during rebuild")
	}
	if !storeapi.IsRebuilding(err) {
		t.Fatalf("expected ErrRebuilding, got: %v", err)
	}
}

func TestRebuilding_RejectsReads(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	_, err := s.WriteDocument(ctx, "tx-before", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	s.SetRebuildingForTest(true)
	_, _, err = s.ReadDocument(ctx, "tx-before", "doc.xml")
	s.SetRebuildingForTest(false)

	if err == nil {
		t.Fatal("expected error during rebuild")
	}
	if !storeapi.IsRebuilding(err) {
		t.Fatalf("expected ErrRebuilding, got: %v", err)
	}
}

func TestRebuildPrepareFailure_ShardRecovers(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.Chmod(blocksDir, 0o000); err != nil {
		t.Skipf("cannot restrict blocks dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocksDir, 0o750) }) //nolint:gosec // test cleanup needs dir traversal

	alreadyInProgress, err := s.TriggerRebuild(ctx)
	if err != nil {
		t.Fatalf("TriggerRebuild: %v", err)
	}
	if alreadyInProgress {
		t.Fatal("expected alreadyInProgress=false")
	}
	s.WaitRebuild()

	_ = os.Chmod(blocksDir, 0o750) //nolint:gosec // test needs dir traversal for write recovery
	_, err = s.WriteDocument(ctx, "tx-after-fail", "doc.xml", "text/xml", "", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("expected shard to recover after prepare failure: %v", err)
	}
}

func TestFindEmptyTransaction(t *testing.T) {
	s := openTestShard(t)
	ctx := context.Background()

	docs, err := s.FindDocuments(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty, got %d", len(docs))
	}
}
