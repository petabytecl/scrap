package shard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestMetadataReadsFailClosedForUnexpectedBlockLoss(t *testing.T) {
	ctx := context.Background()
	s := openUploadTestShard(t, shard.UploadConfig{})

	lostContent := bytes.Repeat([]byte("lost block "), 8)
	if _, err := s.WriteDocument(ctx, "tx-lost", "lost.bin", "application/octet-stream", "", bytes.NewReader(lostContent)); err != nil {
		t.Fatalf("WriteDocument lost: %v", err)
	}
	servedContent := []byte("still serving")
	if _, err := s.WriteDocument(ctx, "tx-served", "served.bin", "application/octet-stream", "", bytes.NewReader(servedContent)); err != nil {
		t.Fatalf("WriteDocument served: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove lost Block: %v", err)
	}

	if _, err := s.HeadDocument(ctx, "tx-lost", "lost.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument lost error = %v, want ErrDataLoss", err)
	}
	if _, err := s.FindDocuments(ctx, "tx-lost"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("FindDocuments lost error = %v, want ErrDataLoss", err)
	}
	if _, _, err := s.ReadDocument(ctx, "tx-lost", "lost.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument lost error = %v, want ErrDataLoss", err)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-served", "served.bin")
	if err != nil {
		t.Fatalf("ReadDocument served: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll served: %v", err)
	}
	if !bytes.Equal(got, servedContent) {
		t.Fatalf("served content = %q, want %q", got, servedContent)
	}
}

// Regression for #467: a corrupt lifecycle marker on a Block whose .blk bytes
// are present and verifiable must not fail reads — the read path re-verifies
// Frame CRC-32C and Document SHA-256, so the side file carries no authority.
func TestReadServesHotBlockDespiteCorruptLifecycleMarker(t *testing.T) {
	ctx := context.Background()
	s := openUploadTestShard(t, shard.UploadConfig{})

	content := bytes.Repeat([]byte("intact bytes "), 4)
	if _, err := s.WriteDocument(ctx, "tx-marker", "doc.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.WriteFile(shard.EvictionMarkerPath(blocksDir, 1), []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	if _, err := s.HeadDocument(ctx, "tx-marker", "doc.bin"); err != nil {
		t.Fatalf("HeadDocument with corrupt marker: %v", err)
	}
	rc, _, err := s.ReadDocument(ctx, "tx-marker", "doc.bin")
	if err != nil {
		t.Fatalf("ReadDocument with corrupt marker: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

// Regression for #467: a corrupt eviction marker on an evicted Block must
// surface as retryable Unavailable, not ErrDataLoss — the Document bytes are
// intact in the Backend; only a per-Member side file is unreadable.
func TestReadEvictedBlockWithCorruptMarkerIsUnavailableNotDataLoss(t *testing.T) {
	ctx := context.Background()
	fsBackend := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     fsBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("cold bytes "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, fsBackend, content)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.WriteFile(shard.EvictionMarkerPath(blocksDir, 1), []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument = %v, must not be ErrDataLoss for a side-file parse failure", err)
	}
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ReadDocument = %v, want ErrUnavailable", err)
	}
	if reason, ok := storeapi.UnavailableReason(err); !ok || reason != storeapi.UnavailableReasonLifecycleMarkerInvalid {
		t.Fatalf("unavailable reason = %q (ok=%t), want %q", reason, ok, storeapi.UnavailableReasonLifecycleMarkerInvalid)
	}

	// Metadata reads take the same stance: side-file parse failure is not loss.
	_, err = s.HeadDocument(ctx, "tx-restore", "doc-1.bin")
	if errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument = %v, must not be ErrDataLoss for a side-file parse failure", err)
	}
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("HeadDocument = %v, want ErrUnavailable", err)
	}
}

// The Unavailable mapping for a corrupt marker requires a committed
// ConfirmUpload proving a durable Backend copy. Without one there is no known
// copy anywhere, so reads must stay ErrDataLoss — same as the valid-marker
// evicted path.
func TestReadCorruptMarkerWithoutConfirmedUploadIsDataLoss(t *testing.T) {
	ctx := context.Background()
	s := openUploadTestShard(t, shard.UploadConfig{})

	content := bytes.Repeat([]byte("no durable copy "), 4)
	if _, err := s.WriteDocument(ctx, "tx-lost", "doc.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-seal", "doc.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument seal: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove Block: %v", err)
	}
	if err := os.WriteFile(shard.EvictionMarkerPath(blocksDir, 1), []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt marker: %v", err)
	}

	if _, _, err := s.ReadDocument(ctx, "tx-lost", "doc.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument = %v, want ErrDataLoss (no committed ConfirmUpload)", err)
	}
	if _, err := s.HeadDocument(ctx, "tx-lost", "doc.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument = %v, want ErrDataLoss (no committed ConfirmUpload)", err)
	}
}

func TestMetadataReadsStayLocalForEvictedBlock(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("metadata only "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	meta, err := s.HeadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("HeadDocument: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("HeadDocument size = %d, want %d", meta.Size, len(content))
	}
	docs, err := s.FindDocuments(ctx, "tx-restore")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "doc-1.bin" {
		t.Fatalf("FindDocuments = %+v, want doc-1.bin", docs)
	}
	if got := countingBackend.getCalls.Load(); got != 0 {
		t.Fatalf("Backend GetObject calls = %d, want 0", got)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if _, err := os.Stat(block.FilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evicted Block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); err != nil {
		t.Fatalf("eviction marker should remain: %v", err)
	}
}

func TestMetadataReadsRequireConfirmedUploadForEvictedBlock(t *testing.T) {
	ctx := context.Background()
	s := openUploadTestShard(t, shard.UploadConfig{})

	content := bytes.Repeat([]byte("marker without catalog "), 4)
	if _, err := s.WriteDocument(ctx, "tx-unconfirmed", "doc.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument unconfirmed: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-next", "doc.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument next: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, shard.EvictionMarker{
		BlockID:         1,
		BackendKey:      "local/shards/0000000000000007/0000000000000001.blk",
		SizeBytes:       123,
		ValidationToken: "validation",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove unconfirmed Block: %v", err)
	}

	if _, err := s.HeadDocument(ctx, "tx-unconfirmed", "doc.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument error = %v, want ErrDataLoss", err)
	}
	if _, err := s.FindDocuments(ctx, "tx-unconfirmed"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("FindDocuments error = %v, want ErrDataLoss", err)
	}
}

func TestMissingIndexFailsClosedWithoutAutomaticRestore(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("missing index "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := os.Remove(block.IdxFilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove lost index: %v", err)
	}

	if _, err := s.HeadDocument(ctx, "tx-restore", "doc-1.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("HeadDocument error = %v, want ErrDataLoss", err)
	}
	if _, err := s.FindDocuments(ctx, "tx-restore"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("FindDocuments error = %v, want ErrDataLoss", err)
	}
	if _, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin"); !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	if got := countingBackend.getCalls.Load(); got != 0 {
		t.Fatalf("Backend GetObject calls = %d, want 0", got)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index stat error = %v, want not exist", err)
	}

	rc, _, err := s.ReadDocument(ctx, "tx-restore-next", "doc-2.bin")
	if err != nil {
		t.Fatalf("ReadDocument unrelated: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll unrelated: %v", err)
	}
	if string(got) != "seal previous" {
		t.Fatalf("unrelated content = %q, want seal previous", got)
	}
}
