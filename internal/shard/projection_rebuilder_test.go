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

func TestProjectionRebuilderRequeuesSealedBlockForRaftConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	blockInfo := writeRebuildBlock(t, blocksDir, 1)
	projection := openProjectionForRebuildTest(t)

	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}

	pending, err := projection.GetPendingUpload(1)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending.BlockID != 1 || pending.ShardID != 7 || pending.SealedSizeBytes != blockInfo.Size() {
		t.Fatalf("pending upload = %+v", pending)
	}
	if _, err := projection.GetConfirmedUpload(1); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestProjectionRebuilderFailsClosedWhenSealedBlockMetadataMissing(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}

	projection := openProjectionForRebuildTest(t)
	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
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

type noopRebuildBackend struct{}

func (noopRebuildBackend) PutObject(context.Context, string, io.Reader, int64, backend.PutOpts) (backend.PutResult, error) {
	return backend.PutResult{}, backend.ErrPermanent
}

func (noopRebuildBackend) HeadObject(context.Context, string) (backend.ObjectMeta, error) {
	return backend.ObjectMeta{}, backend.ErrPermanent
}

func (noopRebuildBackend) GetObject(context.Context, string, backend.GetOpts) (io.ReadCloser, backend.ObjectMeta, error) {
	return nil, backend.ObjectMeta{}, backend.ErrPermanent
}

func (noopRebuildBackend) DeleteObject(context.Context, string) error {
	return backend.ErrPermanent
}

func (noopRebuildBackend) ListObjects(context.Context, string, backend.ListOpts) (backend.ObjectIterator, error) {
	return nil, backend.ErrPermanent
}
