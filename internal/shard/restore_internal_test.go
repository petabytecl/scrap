package shard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
)

func TestPublishVerifiedRestoreSerializesLifecycleMutation(t *testing.T) {
	const blockID uint64 = 1

	s := &Shard{blocksDir: t.TempDir()}
	tmpPath := filepath.Join(s.blocksDir, "restore.tmp")
	if err := os.WriteFile(tmpPath, []byte("restored"), 0o600); err != nil {
		t.Fatalf("write restore tmp: %v", err)
	}
	if err := WriteEvictionMarker(s.blocksDir, EvictionMarker{
		BlockID:         blockID,
		BackendKey:      "cell-a/shards/7/1.blk",
		SizeBytes:       8,
		ValidationToken: "validation",
		EvictedAtUs:     time.Now().UTC().UnixMicro(),
		Trigger:         EvictionTriggerOperatorRequested,
		Reason:          EvictionReasonEvidenceRun,
	}); err != nil {
		t.Fatalf("WriteEvictionMarker: %v", err)
	}

	s.lifecycleMutationMu.Lock()
	done := startRestorePublish(t, s, blockID, tmpPath)
	assertRestorePublishBlocked(t, done, block.FilePath(s.blocksDir, blockID))

	s.lifecycleMutationMu.Unlock()
	waitRestorePublish(t, done)
	assertRestorePublishedForMutationTest(t, s, blockID)
}

func startRestorePublish(t *testing.T, s *Shard, blockID uint64, tmpPath string) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		_, err := s.publishVerifiedRestore(restoreInput{
			confirmed: index.ConfirmedUpload{BlockID: blockID},
			blockPath: block.FilePath(s.blocksDir, blockID),
		}, tmpPath, RestoreReasonRead)
		done <- err
	}()
	return done
}

func assertRestorePublishBlocked(t *testing.T, done <-chan error, blockPath string) {
	t.Helper()

	select {
	case err := <-done:
		t.Fatalf("publish completed while lifecycle mutation lock held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(blockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Block stat while locked = %v, want not exist", err)
	}
}

func waitRestorePublish(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publish restore: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for restore publication")
	}
}

func assertRestorePublishedForMutationTest(t *testing.T, s *Shard, blockID uint64) {
	t.Helper()

	if _, err := os.Stat(block.FilePath(s.blocksDir, blockID)); err != nil {
		t.Fatalf("restored Block stat: %v", err)
	}
	if _, err := os.Stat(EvictionMarkerPath(s.blocksDir, blockID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("eviction marker stat = %v, want not exist", err)
	}
	if _, err := ReadRestoreMarker(s.blocksDir, blockID); err != nil {
		t.Fatalf("ReadRestoreMarker: %v", err)
	}
}
