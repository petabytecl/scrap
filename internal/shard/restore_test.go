package shard_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	"github.com/petabytecl/scrap/internal/shard"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestReadDocumentRestoresEvictedBlockFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("restore me "), 8)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore, content)

	rc, meta, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	defer func() { _ = rc.Close() }()

	assertRestoredDocument(t, rc, meta, content)
	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	assertRestorePublishedHotBlock(t, blocksDir)
}

func TestReadDocumentRestoreBackendTransientReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	backendStore := &failingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		err:     backend.ErrTransient,
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("transient restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrUnavailable) {
		t.Fatalf("ReadDocument error = %v, want ErrUnavailable", err)
	}
	reason, ok := storeapi.UnavailableReason(err)
	if !ok || reason != storeapi.UnavailableReasonBackendRestoreUnavailable {
		t.Fatalf("unavailable reason = %q/%v, want backend_restore_unavailable", reason, ok)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreMissingBackendObjectReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("missing restore "), 4)
	confirmed := stageEvictedConfirmedBlock(ctx, t, s, backendStore, content)
	if err := backendStore.DeleteObject(ctx, confirmed.BlockObject.Key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreCorruptBackendObjectReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &corruptingGetBackend{Backend: backend.NewFS(t.TempDir())}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("corrupt restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreCorruptHeaderReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[0] ^= 0xff
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("header restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreCorruptFrameHeaderReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			frameStart := block.HeaderSize
			data[frameStart] ^= 0xff
			block.RecomputeFramePayloadCRC(data, frameStart)
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("frame header restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreCorruptDocumentSHAReturnsDataLoss(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
			block.RecomputeFramePayloadCRC(data, block.HeaderSize)
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("sha restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, backendStore.Backend, content)

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
	assertRestoreFailureLeftEvicted(t, s)
}

func TestReadDocumentRestoreRequiresCommittedConfirmUpload(t *testing.T) {
	ctx := context.Background()
	s := openTestShard(t)

	content := bytes.Repeat([]byte("no confirm upload "), 4)
	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, shard.EvictionMarker{
		BlockID:         1,
		BackendKey:      "cell-a/shards/0000000000000007/0000000000000001.blk",
		SizeBytes:       123,
		ValidationToken: "validation",
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}

	_, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("ReadDocument error = %v, want ErrDataLoss", err)
	}
}

func TestReadDocumentJoinsConcurrentBlockRestore(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("shared restore "), 8)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	const readers = 5
	var ready sync.WaitGroup
	ready.Add(readers)
	start := make(chan struct{})
	errs := make(chan error, readers)
	for range readers {
		go func() {
			ready.Done()
			<-start
			rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = rc.Close() }()
			got, err := io.ReadAll(rc)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, content) {
				errs <- errors.New("content mismatch")
				return
			}
			errs <- nil
		}()
	}
	ready.Wait()
	close(start)
	<-countingBackend.started
	time.Sleep(25 * time.Millisecond)
	close(countingBackend.release)

	for range readers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent ReadDocument: %v", err)
		}
	}
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
}

func TestReadDocumentSharedRestoreSurvivesLeaderReaderCancellation(t *testing.T) {
	ctx := context.Background()
	countingBackend := newCountingGetBackend(backend.NewFS(t.TempDir()))
	countingBackend.started = make(chan struct{})
	countingBackend.release = make(chan struct{})
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     countingBackend,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("cancelled leader restore "), 4)
	stageEvictedConfirmedBlock(ctx, t, s, countingBackend.Backend, content)

	leaderCtx, cancelLeader := context.WithCancel(ctx)
	leaderErr := make(chan error, 1)
	go func() {
		rc, _, err := s.ReadDocument(leaderCtx, "tx-restore", "doc-1.bin")
		if rc != nil {
			_ = rc.Close()
		}
		leaderErr <- err
	}()

	<-countingBackend.started
	followerErr := make(chan error, 1)
	go func() {
		rc, _, err := s.ReadDocument(ctx, "tx-restore", "doc-1.bin")
		if err != nil {
			followerErr <- err
			return
		}
		defer func() { _ = rc.Close() }()
		got, err := io.ReadAll(rc)
		if err != nil {
			followerErr <- err
			return
		}
		if !bytes.Equal(got, content) {
			followerErr <- errors.New("content mismatch")
			return
		}
		followerErr <- nil
	}()

	time.Sleep(25 * time.Millisecond)
	cancelLeader()
	close(countingBackend.release)

	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader ReadDocument error = %v, want context.Canceled", err)
	}
	if err := <-followerErr; err != nil {
		t.Fatalf("follower ReadDocument: %v", err)
	}
	if got := countingBackend.getCalls.Load(); got != 1 {
		t.Fatalf("Backend GetObject calls = %d, want 1", got)
	}
}

func TestRestoreBlockForRepairRestoresQuarantinedBlockFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("repair restore "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	if err := s.RestoreBlockForRepair(ctx, 1); err != nil {
		t.Fatalf("RestoreBlockForRepair: %v", err)
	}

	assertRepairRestorePublishedHotBlock(t, blocksDir)
}

func TestRestoreBlockForRepairRestoresCorruptQuarantinedIndexFromBackend(t *testing.T) {
	ctx := context.Background()
	backendStore := backend.NewFS(t.TempDir())
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("repair corrupt index "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-index", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-index-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(blocksDir, 1)+block.QuarantineSuffix, []byte("corrupt local index"), 0o600); err != nil {
		t.Fatalf("corrupt quarantined index: %v", err)
	}

	if err := s.RestoreBlockForRepair(ctx, 1); err != nil {
		t.Fatalf("RestoreBlockForRepair: %v", err)
	}

	assertRepairRestorePublishedHotBlock(t, blocksDir)
}

func TestRestoreBlockForRepairCorruptBackendLeavesQuarantine(t *testing.T) {
	ctx := context.Background()
	backendStore := &mutatingGetBackend{
		Backend: backend.NewFS(t.TempDir()),
		mutate: func(data []byte) {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
		},
	}
	s := openUploadTestShard(t, shard.UploadConfig{
		Enabled:     true,
		Backend:     backendStore,
		CellID:      testCellID,
		Concurrency: 1,
	})

	content := bytes.Repeat([]byte("corrupt repair restore "), 8)
	if _, err := s.WriteDocument(ctx, "tx-repair-corrupt", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-repair-corrupt-seal", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}
	waitBackendObject(ctx, t, backendStore.Backend, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore.Backend, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	_ = waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := block.Quarantine(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	err := s.RestoreBlockForRepair(ctx, 1)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("RestoreBlockForRepair error = %v, want ErrDataLoss", err)
	}

	assertRepairRestoreFailureLeftQuarantined(t, blocksDir)
}

func assertRestoredDocument(t *testing.T, rc io.Reader, meta storeapi.DocumentMeta, want []byte) {
	t.Helper()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored content mismatch: got %q want %q", got, want)
	}
	if meta.Size != int64(len(want)) {
		t.Fatalf("meta size = %d, want %d", meta.Size, len(want))
	}
}

func assertRestorePublishedHotBlock(t *testing.T, blocksDir string) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("restored Block not published: %v", err)
	}
	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat error = %v, want not exist", err)
	}
	restore, err := shard.ReadRestoreMarker(blocksDir, 1)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != shard.RestoreSourceBackend || restore.Reason != shard.RestoreReasonRead {
		t.Fatalf("restore marker = %+v, want backend/read", restore)
	}
	lifecycle, err := shard.ClassifyLocalBlock(blocksDir, 1)
	if err != nil {
		t.Fatalf("ClassifyLocalBlock: %v", err)
	}
	if lifecycle.State != shard.LocalBlockStateHot || !lifecycle.ServingAllowed {
		t.Fatalf("lifecycle = %+v, want hot serving-allowed", lifecycle)
	}
}

func assertRepairRestorePublishedHotBlock(t *testing.T, blocksDir string) {
	t.Helper()

	result, err := block.VerifyBlock(block.FilePath(blocksDir, 1), block.IdxFilePath(blocksDir, 1))
	if err != nil {
		t.Fatalf("VerifyBlock: %v", err)
	}
	if len(result.CorruptFrames) != 0 {
		t.Fatalf("restored repair Block has corrupt frames: %+v", result.CorruptFrames)
	}
	if _, err := os.Stat(block.FilePath(blocksDir, 1) + block.QuarantineSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("block quarantine stat = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1) + block.QuarantineSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index quarantine stat = %v, want not exist", err)
	}
	restore, err := shard.ReadRestoreMarker(blocksDir, 1)
	if err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
	if restore.Source != shard.RestoreSourceBackend || restore.Reason != shard.RestoreReasonRepair {
		t.Fatalf("restore marker = %+v, want backend/repair", restore)
	}
}

func assertRepairRestoreFailureLeftQuarantined(t *testing.T, blocksDir string) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat after repair failure = %v, want not exist", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index stat after repair failure = %v, want not exist", err)
	}
	if _, err := os.Stat(block.FilePath(blocksDir, 1) + block.QuarantineSuffix); err != nil {
		t.Fatalf("quarantined Block should remain: %v", err)
	}
	if _, err := os.Stat(block.IdxFilePath(blocksDir, 1) + block.QuarantineSuffix); err != nil {
		t.Fatalf("quarantined index should remain: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.blk.restore-*"))
	if err != nil {
		t.Fatalf("glob repair restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair restore staging files remain: %v", matches)
	}
	matches, err = filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.idx.restore-*"))
	if err != nil {
		t.Fatalf("glob repair index restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("repair index restore staging files remain: %v", matches)
	}
}

func stageEvictedConfirmedBlock(ctx context.Context, t *testing.T, s *shard.Shard, backendStore backend.Backend, content []byte) index.ConfirmedUpload {
	t.Helper()

	if _, err := s.WriteDocument(ctx, "tx-restore", "doc-1.bin", "application/octet-stream", "", bytes.NewReader(content)); err != nil {
		t.Fatalf("WriteDocument doc-1: %v", err)
	}
	if _, err := s.WriteDocument(ctx, "tx-restore-next", "doc-2.bin", "application/octet-stream", "", bytes.NewReader([]byte("seal previous"))); err != nil {
		t.Fatalf("WriteDocument doc-2: %v", err)
	}

	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "blk"))
	waitBackendObject(ctx, t, backendStore, backendObjectKey(1, "idx"))
	waitPendingUploads(t, s, 0)
	confirmed := waitConfirmedUpload(t, s)

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if err := shard.WriteEvictionMarker(blocksDir, evictionMarkerFromConfirmed(confirmed)); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}
	if err := os.Remove(block.FilePath(blocksDir, 1)); err != nil {
		t.Fatalf("remove local Block: %v", err)
	}
	return confirmed
}

func waitConfirmedUpload(t *testing.T, s *shard.Shard) index.ConfirmedUpload {
	t.Helper()

	const blockID uint64 = 1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		confirmed, err := s.ConfirmedUploadForTest(blockID)
		if err == nil {
			return confirmed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for confirmed upload block %d", blockID)
	return index.ConfirmedUpload{}
}

func evictionMarkerFromConfirmed(confirmed index.ConfirmedUpload) shard.EvictionMarker {
	return shard.EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UnixMicro(),
		Trigger:         shard.EvictionTriggerOperatorRequested,
		Reason:          shard.EvictionReasonEvidenceRun,
	}
}

func assertRestoreFailureLeftEvicted(t *testing.T, s *shard.Shard) {
	t.Helper()

	blocksDir := filepath.Join(s.DataDirForTest(), "blocks")
	if _, err := os.Stat(block.FilePath(blocksDir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(shard.EvictionMarkerPath(blocksDir, 1)); err != nil {
		t.Fatalf("eviction marker should remain: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(blocksDir, ".0000000000000001.blk.restore-*"))
	if err != nil {
		t.Fatalf("glob restore staging: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("restore staging files remain: %v", matches)
	}
}

type countingGetBackend struct {
	backend.Backend
	getCalls atomic.Int32
	once     sync.Once
	started  chan struct{}
	release  chan struct{}
}

func newCountingGetBackend(base backend.Backend) *countingGetBackend {
	return &countingGetBackend{Backend: base}
}

func (b *countingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	b.getCalls.Add(1)
	if b.started != nil {
		b.once.Do(func() { close(b.started) })
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, backend.ObjectMeta{}, ctx.Err()
		}
	}
	return b.Backend.GetObject(ctx, key, opts)
}

type failingGetBackend struct {
	backend.Backend
	err error
}

func (b *failingGetBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, b.err
}

type corruptingGetBackend struct {
	backend.Backend
}

func (b *corruptingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return mutateGetObject(ctx, b.Backend, key, opts, func(data []byte) {
		if len(data) > block.HeaderSize+block.FrameHeaderSize {
			data[block.HeaderSize+block.FrameHeaderSize] ^= 0xff
		}
	})
}

type mutatingGetBackend struct {
	backend.Backend
	mutate func([]byte)
}

func (b *mutatingGetBackend) GetObject(ctx context.Context, key string, opts backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return mutateGetObject(ctx, b.Backend, key, opts, b.mutate)
}

func mutateGetObject(
	ctx context.Context,
	base backend.Backend,
	key string,
	opts backend.GetOpts,
	mutate func([]byte),
) (io.ReadCloser, backend.ObjectMeta, error) {
	rc, meta, err := base.GetObject(ctx, key, opts)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, backend.ObjectMeta{}, err
	}
	if mutate != nil {
		mutate(data)
	}
	return io.NopCloser(bytes.NewReader(data)), meta, nil
}
