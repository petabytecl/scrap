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
	openBlockID        uint64
	confirmedUploads   map[uint64]index.ConfirmedUpload
	pendingUploads     map[uint64]index.PendingUpload
	swapStarted        chan struct{}
	releaseSwap        chan struct{}
	swapOnce           sync.Once
	idxNil             bool
	swapErr            error
	appliedIndex       uint64
	contentQuarantines []index.ContentQuarantine
}

func (s *projectionRebuildCoreStub) currentOpenBlockIDLocked() uint64 {
	return s.openBlockID
}

func (s *projectionRebuildCoreStub) projectionAppliedIndex() (uint64, bool) {
	return s.appliedIndex, true
}

func (s *projectionRebuildCoreStub) copyContentSafetyIntoLocked(dst *index.Index) error {
	for _, q := range s.contentQuarantines {
		if err := dst.PutContentQuarantine(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *projectionRebuildCoreStub) confirmedUploadForRebuildLocked(blockID uint64) (index.ConfirmedUpload, error) {
	confirmed, ok := s.confirmedUploads[blockID]
	if !ok {
		return index.ConfirmedUpload{}, index.ErrConfirmedUploadNotFound
	}
	return confirmed, nil
}

func (s *projectionRebuildCoreStub) pendingUploadForRebuildLocked(blockID uint64) (index.PendingUpload, error) {
	upload, ok := s.pendingUploads[blockID]
	if !ok {
		return index.PendingUpload{}, index.ErrPendingUploadNotFound
	}
	return upload, nil
}

func (s *projectionRebuildCoreStub) finalizeAndSwapRebuiltProjection(finalize func() error, _, _, _ string) (bool, error) {
	s.swapOnce.Do(func() {
		if s.swapStarted != nil {
			close(s.swapStarted)
		}
	})
	if s.releaseSwap != nil {
		<-s.releaseSwap
	}
	if err := finalize(); err != nil {
		return s.idxNil, err
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

// H-05 / ADR 0034: an orphan .idx without a companion .blk must not become
// Document visibility during Projection rebuild.
func TestProjectionRebuilderRefusesOrphanIndexWithoutBlock(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write orphan idx: %v", err)
	}
	projection := openProjectionForRebuildTest(t)
	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{}, nil)
	if _, err := r.rebuildProjectionInto(projection); err == nil {
		t.Fatal("rebuildProjectionInto succeeded with orphan .idx, want error")
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
	blockInfo := writeRebuildBlock(t, blocksDir)
	projection := openProjectionForRebuildTest(t)

	r := newProjectionRebuilder(&projectionRebuildCoreStub{}, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
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

func TestProjectionRebuilderPreservesHotConfirmedBlock(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	writeRebuildBlock(t, blocksDir)
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}
	projection := openProjectionForRebuildTest(t)
	confirmed := confirmedUploadForEvictionApply(1, 1024)
	core := &projectionRebuildCoreStub{
		confirmedUploads: map[uint64]index.ConfirmedUpload{
			1: confirmed,
		},
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}

	if _, err := projection.GetPendingUpload(1); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	got, err := projection.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload = %+v, want %+v", got, confirmed)
	}
}

func TestProjectionRebuilderPreservesPendingRewrapUploadOverConfirmedAuthority(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	blockInfo := writeRebuildBlock(t, blocksDir)
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}

	replacement := index.PendingUpload{
		BlockID:          1,
		ShardID:          7,
		SealedSizeBytes:  blockInfo.Size(),
		SealedAtUs:       1716700002000000,
		UploadGeneration: 1716700002000000,
	}
	core := &projectionRebuildCoreStub{
		confirmedUploads: map[uint64]index.ConfirmedUpload{
			1: confirmedUploadForEvictionApply(1, blockInfo.Size()),
		},
		pendingUploads: map[uint64]index.PendingUpload{
			1: replacement,
		},
	}
	projection := openProjectionForRebuildTest(t)
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}

	pending, err := projection.GetPendingUpload(1)
	if err != nil {
		t.Fatalf("GetPendingUpload: %v", err)
	}
	if pending != replacement {
		t.Fatalf("pending upload = %+v, want replacement %+v", pending, replacement)
	}
	if _, err := projection.GetConfirmedUpload(1); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestProjectionRebuilderPreservesHotConfirmedBlockWhenUploadsDisabled(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	writeRebuildBlock(t, blocksDir)
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}
	projection := openProjectionForRebuildTest(t)
	confirmed := confirmedUploadForEvictionApply(1, 1024)
	core := &projectionRebuildCoreStub{
		confirmedUploads: map[uint64]index.ConfirmedUpload{
			1: confirmed,
		},
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}
	if _, err := projection.GetPendingUpload(1); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	got, err := projection.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload = %+v, want %+v", got, confirmed)
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

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err == nil {
		t.Fatal("rebuildUploadOutbox succeeded without sealed Block metadata")
	}
	if _, err := projection.GetConfirmedUpload(1); !errors.Is(err, index.ErrConfirmedUploadNotFound) {
		t.Fatalf("GetConfirmedUpload error = %v, want ErrConfirmedUploadNotFound", err)
	}
}

func TestProjectionRebuilderSkipsEvictedConfirmedBlock(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}

	projection := openProjectionForRebuildTest(t)
	confirmed := confirmedUploadForEvictionApply(1, 1024)
	if err := WriteEvictionMarker(blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	core := &projectionRebuildCoreStub{
		confirmedUploads: map[uint64]index.ConfirmedUpload{
			1: confirmed,
		},
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}
	if _, err := projection.GetPendingUpload(1); !errors.Is(err, index.ErrPendingUploadNotFound) {
		t.Fatalf("GetPendingUpload error = %v, want ErrPendingUploadNotFound", err)
	}
	got, err := projection.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got.BlockObject.Key != confirmed.BlockObject.Key || got.BlockObject.ValidationToken == "" {
		t.Fatalf("confirmed upload = %+v, want copied Backend authority", got)
	}
}

func TestProjectionRebuilderPreservesEvictedConfirmedBlockWhenUploadsDisabled(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}

	projection := openProjectionForRebuildTest(t)
	confirmed := confirmedUploadForEvictionApply(1, 1024)
	if err := WriteEvictionMarker(blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	core := &projectionRebuildCoreStub{
		confirmedUploads: map[uint64]index.ConfirmedUpload{
			1: confirmed,
		},
	}
	r := newProjectionRebuilder(core, dataDir, blocksDir, 7, UploadConfig{}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{1}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}
	got, err := projection.GetConfirmedUpload(1)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload = %+v, want %+v", got, confirmed)
	}
}

func TestProjectionRebuilderPreservesEvictedCommittedBlockAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	blocksDir := filepath.Join(dataDir, "blocks")
	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, fmt.Sprintf("%016x.idx", uploadApplyTestBlockID)), []byte("index bytes"), 0o600); err != nil {
		t.Fatalf("write Block index: %v", err)
	}

	idx := openApplyTestIndex(t)
	if err := idx.PutPendingUpload(index.PendingUpload{
		BlockID:         uploadApplyTestBlockID,
		ShardID:         7,
		SealedSizeBytes: 67108864,
		SealedAtUs:      1716700000000000,
	}); err != nil {
		t.Fatalf("PutPendingUpload: %v", err)
	}
	s := shardForApplyTest(t, idx)
	s.blocksDir = blocksDir
	confirm := confirmUploadCommandForApplyTest()
	if err := s.applyConfirmUpload(confirm); err != nil {
		t.Fatalf("applyConfirmUpload: %v", err)
	}
	confirmed := confirmedUploadForApplyTest(confirm.GetConfirmedAtUs(), shardApplyValidationValue("block"), shardApplyValidationValue("index"))
	if err := WriteEvictionMarker(blocksDir, EvictionMarker{
		BlockID:         confirmed.BlockID,
		BackendKey:      confirmed.BlockObject.Key,
		SizeBytes:       confirmed.BlockObject.SizeBytes,
		ValidationToken: confirmed.BlockObject.ValidationToken,
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	restarted := &Shard{blocksDir: blocksDir}
	projection := openProjectionForRebuildTest(t)
	r := newProjectionRebuilder(restarted, dataDir, blocksDir, 7, UploadConfig{
		Enabled: true,
		Backend: noopRebuildBackend{},
		CellID:  "cell-a",
	}, nil)

	if err := r.rebuildUploadOutbox(projection, []uint64{uploadApplyTestBlockID}, nil); err != nil {
		t.Fatalf("rebuildUploadOutbox: %v", err)
	}
	got, err := projection.GetConfirmedUpload(uploadApplyTestBlockID)
	if err != nil {
		t.Fatalf("GetConfirmedUpload: %v", err)
	}
	if got != confirmed {
		t.Fatalf("confirmed upload after restart = %+v, want %+v", got, confirmed)
	}
}

func writeRebuildBlock(t *testing.T, blocksDir string) os.FileInfo {
	t.Helper()

	if err := os.MkdirAll(blocksDir, 0o750); err != nil {
		t.Fatalf("mkdir blocks dir: %v", err)
	}
	path := filepath.Join(blocksDir, "0000000000000001.blk")
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

func TestRecoverProjectionSwapDirsAbortsOnUnreadableProjection(t *testing.T) {
	dataDir := t.TempDir()

	// A pre-rebuild backup from an interrupted swap is the only surviving copy
	// of the projection and must not be destroyed.
	backup := filepath.Join(dataDir, "pebble.previous-1")
	if err := os.MkdirAll(backup, 0o750); err != nil {
		t.Fatalf("mkdir backup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backup, "MANIFEST"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write backup file: %v", err)
	}

	// Stand in for an EIO/EACCES read of the live projection: a plain file at
	// the projection path makes os.ReadDir fail with ENOTDIR, which is not
	// os.IsNotExist.
	pebbleDir := filepath.Join(dataDir, "pebble")
	if err := os.WriteFile(pebbleDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write pebble stand-in file: %v", err)
	}

	if err := recoverProjectionSwapDirs(dataDir, pebbleDir); err == nil {
		t.Fatal("recoverProjectionSwapDirs succeeded on unreadable projection, want error")
	}

	// The backup must survive so a later recovery can still restore it.
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup removed on a failing-disk read: %v", err)
	}
}
