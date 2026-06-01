package shard

import (
	"context"
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

func TestPublishVerifiedRestoreRecordsHealthAfterPartialPublish(t *testing.T) {
	const blockID uint64 = 1

	s := &Shard{
		blocksDir:            t.TempDir(),
		evictionHealthBlocks: make(map[uint64]evictionHealthBlockContribution),
	}
	tmpPath := filepath.Join(s.blocksDir, "restore.tmp")
	if err := os.WriteFile(tmpPath, []byte("restored"), 0o600); err != nil {
		t.Fatalf("write restore tmp: %v", err)
	}
	if err := os.WriteFile(block.IdxFilePath(s.blocksDir, blockID), []byte("idx"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
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

	published, err := s.publishVerifiedRestore(restoreInput{
		confirmed: index.ConfirmedUpload{BlockID: blockID},
		blockPath: block.FilePath(s.blocksDir, blockID),
		indexPath: block.IdxFilePath(s.blocksDir, blockID),
	}, tmpPath, "")
	if !published || err == nil {
		t.Fatalf("publishVerifiedRestore published=%v err=%v, want published marker error", published, err)
	}

	got, err := s.EvictionHealthSnapshot(context.Background())
	if err != nil {
		t.Fatalf("EvictionHealthSnapshot: %v", err)
	}
	if got.HotCleanupNeededBlocks != 1 {
		t.Fatalf("hot cleanup needed = %d, want 1", got.HotCleanupNeededBlocks)
	}
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

func TestPublishRepairedBlockDoesNotConsumeIndexWhenBlockPublishFails(t *testing.T) {
	const blockID uint64 = 1

	blocksDir := t.TempDir()
	blockPath := block.FilePath(blocksDir, blockID)
	tmpBlockPath := filepath.Join(blocksDir, "repair.blk.tmp")
	tmpIndexPath := filepath.Join(blocksDir, "repair.idx.tmp")
	idxFinal := block.IdxFilePath(blocksDir, blockID)
	idxQuarantine := idxFinal + block.QuarantineSuffix

	if err := os.WriteFile(tmpBlockPath, []byte("restored"), 0o600); err != nil {
		t.Fatalf("write repair tmp: %v", err)
	}
	if err := os.WriteFile(tmpIndexPath, []byte("restored idx"), 0o600); err != nil {
		t.Fatalf("write repair index tmp: %v", err)
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
	}, tmpBlockPath, tmpIndexPath)
	if err == nil {
		t.Fatal("expected publish failure")
	}
	if _, statErr := os.Stat(idxQuarantine); statErr != nil {
		t.Fatalf("quarantined index stat after rollback: %v", statErr)
	}
	if _, statErr := os.Stat(idxFinal); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final index stat after rollback = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(tmpIndexPath); statErr != nil {
		t.Fatalf("repair index tmp stat after block publish failure: %v", statErr)
	}
}

func TestPublishRepairedBlockRollsBackBlockOnIndexPublishFailure(t *testing.T) {
	const blockID uint64 = 1

	blocksDir := t.TempDir()
	blockPath := block.FilePath(blocksDir, blockID)
	tmpBlockPath := filepath.Join(blocksDir, "repair.blk.tmp")
	tmpIndexPath := filepath.Join(blocksDir, "repair.idx.tmp")
	idxFinal := block.IdxFilePath(blocksDir, blockID)
	idxQuarantine := idxFinal + block.QuarantineSuffix

	if err := os.WriteFile(tmpBlockPath, []byte("restored"), 0o600); err != nil {
		t.Fatalf("write repair block tmp: %v", err)
	}
	if err := os.WriteFile(tmpIndexPath, []byte("restored idx"), 0o600); err != nil {
		t.Fatalf("write repair index tmp: %v", err)
	}
	if err := os.WriteFile(blockPath+block.QuarantineSuffix, []byte("quarantined"), 0o600); err != nil {
		t.Fatalf("write quarantined Block: %v", err)
	}
	if err := os.WriteFile(idxQuarantine, []byte("idx"), 0o600); err != nil {
		t.Fatalf("write quarantined index: %v", err)
	}
	if err := os.Mkdir(idxFinal, 0o750); err != nil {
		t.Fatalf("mkdir index publish collision: %v", err)
	}

	err := publishRepairedBlock(restoreInput{
		confirmed: index.ConfirmedUpload{BlockID: blockID},
		blockPath: blockPath,
	}, tmpBlockPath, tmpIndexPath)
	if err == nil {
		t.Fatal("expected publish failure")
	}
	if _, statErr := os.Stat(blockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final Block stat after rollback = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(blockPath + block.QuarantineSuffix); statErr != nil {
		t.Fatalf("quarantined Block stat after rollback: %v", statErr)
	}
	if _, statErr := os.Stat(idxQuarantine); statErr != nil {
		t.Fatalf("quarantined index stat after rollback: %v", statErr)
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
