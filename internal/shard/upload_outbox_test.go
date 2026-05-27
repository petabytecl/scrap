package shard_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/shard"
)

const (
	testCellID  = "cell-a"
	testShardID = 7
)

func TestShardSealMaterializesPendingUpload(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-upload-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-upload-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}

	uploads := waitPendingUploads(t, s, 1)
	if uploads[0].BlockID != 1 {
		t.Fatalf("pending BlockID = %d, want 1", uploads[0].BlockID)
	}
	if uploads[0].ShardID != testShardID {
		t.Fatalf("pending ShardID = %d, want %d", uploads[0].ShardID, testShardID)
	}
	if uploads[0].SealedSizeBytes <= 0 {
		t.Fatalf("pending SealedSizeBytes = %d, want > 0", uploads[0].SealedSizeBytes)
	}
}

func TestShardConfirmUploadClearsPendingUpload(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-upload-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-upload-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitPendingUploads(t, s, 1)

	if err := s.ConfirmUploadForTest(ctx, 1, "cell-a/shards/0000000000000007/0000000000000001", "etag-1"); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitPendingUploads(t, s, 0)
}

func TestShardUploadProcessorUploadsSealedBlocks(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 2,
	})

	for i := 1; i <= 4; i++ {
		txID := fmt.Sprintf("tx-upload-%d", i)
		docName := fmt.Sprintf("doc-%d.bin", i)
		if _, err := s.WriteDocument(ctx, txID, docName, "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte{byte(i)}, 64))); err != nil {
			t.Fatalf("WriteDocument %s: %v", docName, err)
		}
	}

	for blockID := uint64(1); blockID <= 3; blockID++ {
		waitBackendObject(ctx, t, backendStore, backendObjectKey(blockID, "blk"))
		waitBackendObject(ctx, t, backendStore, backendObjectKey(blockID, "idx"))
	}
	waitPendingUploads(t, s, 0)
}

func TestShardUploadProcessorResumesPendingUploadAfterReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	backendStore := backend.NewFS(t.TempDir())

	s := openUploadTestShardInDir(t, dataDir, shard.UploadConfig{
		Enabled:     true,
		Backend:     transientBackend{},
		CellID:      testCellID,
		Concurrency: 1,
	})
	if _, err := s.WriteDocument(ctx, "tx-upload-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-upload-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitPendingUploads(t, s, 1)
	if err := s.Close(); err != nil {
		t.Fatalf("Close first shard: %v", err)
	}

	reopened := openUploadTestShardInDir(t, dataDir, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})
	t.Cleanup(func() { _ = reopened.Close() })
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, reopened, 0)
}

func openUploadTestShard(t *testing.T, upload shard.UploadConfig) *shard.Shard {
	t.Helper()

	s := openUploadTestShardInDir(t, t.TempDir(), upload)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openUploadTestShardInDir(t *testing.T, dataDir string, upload shard.UploadConfig) *shard.Shard {
	t.Helper()

	s, err := shard.Open(shard.Config{
		DataDir:        dataDir,
		ShardID:        testShardID,
		RaftID:         1,
		Peers:          map[uint64]string{1: "localhost:9091"},
		BlockSealSize:  41,
		TickInterval:   10 * time.Millisecond,
		BootstrapGrace: time.Second,
		Upload:         upload,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
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

func waitPendingUploads(t *testing.T, s *shard.Shard, want int) []shard.PendingUpload {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		uploads, err := s.PendingUploadsForTest()
		if err != nil {
			t.Fatalf("PendingUploadsForTest: %v", err)
		}
		if len(uploads) == want {
			return uploads
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending uploads", want)
	return nil
}

func waitBackendObject(ctx context.Context, t *testing.T, store backend.Backend, key string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		meta, err := store.HeadObject(ctx, key)
		if err == nil && meta.Size > 0 && meta.ETag != "" {
			return
		}
		if err != nil && !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("HeadObject %s: %v", key, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for backend object %s", key)
}

func backendObjectKey(blockID uint64, ext string) string {
	return fmt.Sprintf("%s/shards/%016x/%016x.%s", testCellID, testShardID, blockID, ext)
}

type transientBackend struct{}

func (transientBackend) PutObject(context.Context, string, io.Reader, int64, backend.PutOpts) (backend.PutResult, error) {
	return backend.PutResult{}, backend.ErrTransient
}

func (transientBackend) HeadObject(context.Context, string) (backend.ObjectMeta, error) {
	return backend.ObjectMeta{}, backend.ErrTransient
}

func (transientBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrTransient
}

func (transientBackend) DeleteObject(context.Context, string) error {
	return backend.ErrTransient
}

func (transientBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrTransient
}
