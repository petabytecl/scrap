package shard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/index"
)

type projectionRebuildCoreStub struct {
	openBlockID uint64
	swapStarted chan struct{}
	releaseSwap chan struct{}
	swapOnce    sync.Once
	idxNil      bool
	swapErr     error
}

func (s *projectionRebuildCoreStub) currentOpenBlockID() uint64 {
	return s.openBlockID
}

func (s *projectionRebuildCoreStub) swapRebuiltProjection(_, _, _ string) (bool, error) {
	s.swapOnce.Do(func() {
		if s.swapStarted != nil {
			close(s.swapStarted)
		}
	})
	if s.releaseSwap != nil {
		<-s.releaseSwap
	}
	return s.idxNil, s.swapErr
}

func TestProjectionRebuilderListBlockIndexIDs(t *testing.T) {
	blocksDir := t.TempDir()
	for _, name := range []string{
		"000000000000000a.idx",
		"0000000000000002.idx",
		"0000000000000003.blk",
		"not-a-block.idx",
	} {
		if err := os.WriteFile(filepath.Join(blocksDir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, t.TempDir(), blocksDir, 7, UploadConfig{}, nil)
	got, err := r.listBlockIndexIDs()
	if err != nil {
		t.Fatalf("listBlockIndexIDs: %v", err)
	}
	want := []uint64{2, 10}
	if len(got) != len(want) {
		t.Fatalf("block IDs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("block IDs = %v, want %v", got, want)
		}
	}
}

func TestProjectionRebuilderTriggerWaitAndInProgress(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}

	core := &projectionRebuildCoreStub{
		swapStarted: make(chan struct{}),
		releaseSwap: make(chan struct{}),
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{}, nil)

	alreadyInProgress, err := r.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if alreadyInProgress {
		t.Fatal("first trigger should start rebuild")
	}

	select {
	case <-core.swapStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projection swap")
	}
	if !r.InProgress() {
		t.Fatal("rebuild should be in progress while swap is blocked")
	}

	alreadyInProgress, err = r.Trigger(context.Background())
	if err != nil {
		t.Fatalf("second Trigger: %v", err)
	}
	if !alreadyInProgress {
		t.Fatal("second trigger should report rebuild already in progress")
	}

	close(core.releaseSwap)
	r.Wait()
	if r.InProgress() {
		t.Fatal("rebuild should not remain in progress after Wait")
	}
}

func TestProjectionRebuilderLeavesShardUnavailableWhenSwapLosesIndex(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}

	core := &projectionRebuildCoreStub{
		idxNil:  true,
		swapErr: errors.New("swap failed"),
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{}, nil)

	alreadyInProgress, err := r.Trigger(context.Background())
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if alreadyInProgress {
		t.Fatal("first trigger should start rebuild")
	}

	r.Wait()
	if !r.InProgress() {
		t.Fatal("degraded rebuild should keep shard marked rebuilding")
	}
	r.setInProgressForTest(false)
}

func TestProjectionRebuilderCatalogsFullyUploadedBlock(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	blockInfo := writeRebuildBlock(t, blocksDir, 1)
	projection := openProjectionForRebuildTest(t)

	prefix := backendKeyPrefix("cell-a", 7, 1)
	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: headOnlyBackend{objects: map[string]backend.ObjectMeta{
			prefix + ".blk": {Size: blockInfo.Size(), ETag: "block-validation"},
			prefix + ".idx": {Size: 4096, ETag: "index-validation"},
		}},
		CellID: "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}

	confirmed := requireConfirmedUpload(t, projection, 1)
	assertRebuiltConfirmedUpload(t, projection, confirmed, prefix, blockInfo.Size())
}

func TestProjectionRebuilderFailsClosedWhenUploadedBlockMetadataMissing(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}

	projection := openProjectionForRebuildTest(t)
	prefix := backendKeyPrefix("cell-a", 7, 1)
	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: headOnlyBackend{objects: map[string]backend.ObjectMeta{
			prefix + ".blk": {Size: 11, ETag: "block-validation"},
			prefix + ".idx": {Size: 4096, ETag: "index-validation"},
		}},
		CellID: "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}); err == nil {
		t.Fatal("rebuildUploadOutbox succeeded without sealed Block metadata")
	}
	if _, err := projection.GetConfirmedUpload(1); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func writeRebuildBlock(t *testing.T, blocksDir string, blockID uint64) os.FileInfo {
	t.Helper()

	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	path := filepath.Join(blocksDir, fmt.Sprintf("%016x.blk", blockID))
	if err := os.WriteFile(path, []byte("sealed block"), 0o600); err != nil {
		t.Fatalf("write block: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat block: %v", err)
	}
	return info
}

func openProjectionForRebuildTest(t *testing.T) *index.Index {
	t.Helper()

	projection, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open projection: %v", err)
	}
	t.Cleanup(func() { _ = projection.Close() })
	return projection
}

func requireConfirmedUpload(t *testing.T, projection *index.Index, blockID uint64) index.ConfirmedUpload {
	t.Helper()

	confirmed, err := projection.GetConfirmedUpload(blockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	return confirmed
}

func assertRebuiltConfirmedUpload(t *testing.T, projection *index.Index, confirmed index.ConfirmedUpload, prefix string, sealedSize int64) {
	t.Helper()

	assertRebuiltConfirmedIdentity(t, confirmed, sealedSize)
	if confirmed.ConfirmedAtUs <= 0 {
		t.Fatalf("ConfirmedAtUs = %d, want > 0", confirmed.ConfirmedAtUs)
	}
	assertRebuiltBackendObject(t, "BlockObject", confirmed.BlockObject, prefix+".blk", sealedSize, "block-validation")
	assertRebuiltBackendObject(t, "IndexObject", confirmed.IndexObject, prefix+".idx", 4096, "index-validation")
	if _, err := projection.GetPendingUpload(1); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
}

func assertRebuiltConfirmedIdentity(t *testing.T, confirmed index.ConfirmedUpload, sealedSize int64) {
	t.Helper()

	if confirmed.BlockID != 1 {
		t.Fatalf("BlockID = %d, want 1", confirmed.BlockID)
	}
	if confirmed.ShardID != 7 {
		t.Fatalf("ShardID = %d, want 7", confirmed.ShardID)
	}
	if confirmed.SealedSizeBytes != sealedSize {
		t.Fatalf("SealedSizeBytes = %d, want %d", confirmed.SealedSizeBytes, sealedSize)
	}
}

func assertRebuiltBackendObject(t *testing.T, label string, got index.BackendObjectMetadata, key string, size int64, validation string) {
	t.Helper()

	if got.Key != key {
		t.Fatalf("%s.Key = %q, want %q", label, got.Key, key)
	}
	if got.SizeBytes != size {
		t.Fatalf("%s.SizeBytes = %d, want %d", label, got.SizeBytes, size)
	}
	if got.ValidationToken != validation {
		t.Fatalf("%s.ValidationToken = %q, want %q", label, got.ValidationToken, validation)
	}
}

type headOnlyBackend struct {
	objects map[string]backend.ObjectMeta
}

func (b headOnlyBackend) PutObject(context.Context, string, io.Reader, int64, backend.PutOpts) (backend.PutResult, error) {
	return backend.PutResult{}, backend.ErrPermanent
}

func (b headOnlyBackend) HeadObject(_ context.Context, key string) (backend.ObjectMeta, error) {
	meta, ok := b.objects[key]
	if !ok {
		return backend.ObjectMeta{}, backend.ErrNotFound
	}
	return meta, nil
}

func (b headOnlyBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (b headOnlyBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (b headOnlyBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
}
