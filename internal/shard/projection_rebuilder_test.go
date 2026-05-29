package shard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
