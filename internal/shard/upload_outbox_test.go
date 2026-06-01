package shard_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/index"
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

	pending := waitPendingUploads(t, s, 1)[0]
	if err := s.ConfirmUploadForTest(ctx, confirmedUploadForTest(pending.SealedSizeBytes)); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}
	waitPendingUploads(t, s, 0)
}

func TestShardConfirmUploadCatalogsConfirmedUploadAndClearsPendingUpload(t *testing.T) {
	s := openUploadTestShard(t, shard.UploadConfig{Enabled: true})
	ctx := context.Background()

	if _, err := s.WriteDocument(ctx, "tx-upload-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-upload-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	pending := waitPendingUploads(t, s, 1)[0]

	confirmed := confirmedUploadForTest(pending.SealedSizeBytes)
	if err := s.ConfirmUploadForTest(ctx, confirmed); err != nil {
		t.Fatalf("ConfirmUploadForTest: %v", err)
	}

	waitPendingUploads(t, s, 0)
	got, err := s.ConfirmedUploadForTest(1)
	if err != nil {
		t.Fatalf("ConfirmedUploadForTest: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload = %+v, want %+v", got, confirmed)
	}
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

func TestShardUploadProcessorRejectsEmptyValidationToken(t *testing.T) {
	ctx := context.Background()
	backendStore := newEmptyValidationBackend()
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	if _, err := s.WriteDocument(ctx, "tx-upload-1", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(bytes.Repeat([]byte("a"), 64))); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-upload-2", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("b"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}

	backendStore.waitHeadCalls(t, 1)
	waitPendingUploads(t, s, 1)
	if _, err := s.ConfirmedUploadForTest(1); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("ConfirmedUploadForTest error = %v, want ErrConfirmedUploadNotFound", err)
	}
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

func confirmedUploadForTest(blockSize int64) index.ConfirmedUpload {
	const blockID = 1
	prefix := fmt.Sprintf("%s/shards/%016x/%016x", testCellID, testShardID, blockID)
	return index.ConfirmedUpload{
		BlockID:         blockID,
		ShardID:         testShardID,
		ConfirmedAtUs:   1716700001000000,
		SealedSizeBytes: blockSize,
		BlockObject: index.BackendObjectMetadata{
			Key:             prefix + ".blk",
			SizeBytes:       blockSize,
			ValidationToken: shardValidationValue("block"),
		},
		IndexObject: index.BackendObjectMetadata{
			Key:             prefix + ".idx",
			SizeBytes:       4096,
			ValidationToken: shardValidationValue("index"),
		},
	}
}

func shardValidationValue(kind string) string {
	return kind + "-validation"
}

type emptyValidationBackend struct {
	mu        sync.Mutex
	sizes     map[string]int64
	headCalls atomic.Int32
}

func newEmptyValidationBackend() *emptyValidationBackend {
	return &emptyValidationBackend{sizes: make(map[string]int64)}
}

func (b *emptyValidationBackend) PutObject(_ context.Context, key string, body io.Reader, size int64, _ backend.PutOpts) (backend.PutResult, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return backend.PutResult{}, fmt.Errorf("%w: read object: %w", backend.ErrPermanent, err)
	}

	b.mu.Lock()
	b.sizes[key] = size
	b.mu.Unlock()

	return backend.PutResult{Size: size}, nil
}

func (b *emptyValidationBackend) HeadObject(_ context.Context, key string) (backend.ObjectMeta, error) {
	b.mu.Lock()
	size, ok := b.sizes[key]
	b.mu.Unlock()

	if !ok {
		return backend.ObjectMeta{}, backend.ErrNotFound
	}
	b.headCalls.Add(1)
	return backend.ObjectMeta{Size: size}, nil
}

func (b *emptyValidationBackend) waitHeadCalls(t *testing.T, want int32) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.headCalls.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d backend HEAD calls", want)
}

func (b *emptyValidationBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (b *emptyValidationBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (b *emptyValidationBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
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
