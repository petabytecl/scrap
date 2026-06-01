package shard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/block"
	"github.com/petabytecl/scrap/internal/index"
	storeapi "github.com/petabytecl/scrap/internal/store"
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

func TestRequireQuarantinedRepairFilesFailsClosed(t *testing.T) {
	const blockID uint64 = 1

	blocksDir := t.TempDir()
	err := requireQuarantinedRepairFiles(blocksDir, blockID)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("missing quarantine error = %v, want ErrDataLoss", err)
	}

	if err := os.WriteFile(block.FilePath(blocksDir, blockID)+block.QuarantineSuffix, []byte("blk"), 0o600); err != nil {
		t.Fatalf("write quarantined Block: %v", err)
	}
	err = requireQuarantinedRepairFiles(blocksDir, blockID)
	if !errors.Is(err, storeapi.ErrDataLoss) {
		t.Fatalf("missing quarantine index error = %v, want ErrDataLoss", err)
	}

	if err := os.WriteFile(block.IdxFilePath(blocksDir, blockID)+block.QuarantineSuffix, []byte("idx"), 0o600); err != nil {
		t.Fatalf("write quarantined index: %v", err)
	}
	if err := requireQuarantinedRepairFiles(blocksDir, blockID); err != nil {
		t.Fatalf("requireQuarantinedRepairFiles: %v", err)
	}
}

func TestPublishRepairedBlockRollsBackIndexOnBlockPublishFailure(t *testing.T) {
	const blockID uint64 = 1

	blocksDir := t.TempDir()
	blockPath := block.FilePath(blocksDir, blockID)
	tmpPath := filepath.Join(blocksDir, "repair.tmp")
	idxFinal := block.IdxFilePath(blocksDir, blockID)
	idxQuarantine := idxFinal + block.QuarantineSuffix

	if err := os.WriteFile(tmpPath, []byte("restored"), 0o600); err != nil {
		t.Fatalf("write repair tmp: %v", err)
	}
	if err := os.WriteFile(idxQuarantine, []byte("idx"), 0o600); err != nil {
		t.Fatalf("write quarantined index: %v", err)
	}
	if err := os.Mkdir(blockPath, 0o750); err != nil {
		t.Fatalf("mkdir block publish collision: %v", err)
	}

	err := publishRepairedBlock(restoreInput{
		confirmed: index.ConfirmedUpload{BlockID: blockID},
		blockPath: blockPath,
	}, tmpPath)
	if err == nil {
		t.Fatal("expected publish failure")
	}
	if _, statErr := os.Stat(idxQuarantine); statErr != nil {
		t.Fatalf("quarantined index stat after rollback: %v", statErr)
	}
	if _, statErr := os.Stat(idxFinal); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final index stat after rollback = %v, want not exist", statErr)
	}
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
