package shard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/index"
)

func TestWriteAndReadSealIntent(t *testing.T) {
	dir := t.TempDir()
	s := &Shard{blocksDir: dir, shardID: 7}
	if err := s.writeSealIntentLocked(3, 4096); err != nil {
		t.Fatalf("writeSealIntentLocked: %v", err)
	}
	intent, err := readSealIntent(dir, 3)
	if err != nil {
		t.Fatalf("readSealIntent: %v", err)
	}
	if intent.BlockID != 3 || intent.ShardID != 7 || intent.SealedSizeBytes != 4096 {
		t.Fatalf("intent = %+v", intent)
	}
	if intent.SealedAtUs <= 0 {
		t.Fatal("SealedAtUs must be set")
	}
}

func TestReconcileClosedBlockFromSealIntent(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	blocksDir := t.TempDir()
	s := &Shard{
		blocksDir:    blocksDir,
		shardID:      7,
		idx:          idx,
		upload:       UploadConfig{Enabled: true},
		blockUploads: newBlockUploadLifecycle(),
	}
	if err := s.writeSealIntentLocked(1, 128); err != nil {
		t.Fatalf("writeSealIntentLocked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.blk"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write blk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blocksDir, "0000000000000001.idx"), []byte("y"), 0o600); err != nil {
		t.Fatalf("write idx: %v", err)
	}

	if err := s.reconcileClosedBlockLocked(1); err != nil {
		t.Fatalf("reconcileClosedBlockLocked: %v", err)
	}
	if got := s.uploadOutboxLocked().UploadObligationCount(); got != 1 {
		t.Fatalf("upload obligations = %d, want 1", got)
	}
}

func TestReconcileSkipsConfirmedUpload(t *testing.T) {
	idx, err := index.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.PutConfirmedUpload(index.ConfirmedUpload{
		BlockID:         1,
		ShardID:         7,
		SealedSizeBytes: 10,
		ConfirmedAtUs:   time.Now().UnixMicro(),
		BlockObject:     index.BackendObjectMetadata{Key: "k.blk", SizeBytes: 10, ValidationToken: "e"},
		IndexObject:     index.BackendObjectMetadata{Key: "k.idx", SizeBytes: 1, ValidationToken: "e"},
	}); err != nil {
		t.Fatalf("PutConfirmedUpload: %v", err)
	}

	blocksDir := t.TempDir()
	s := &Shard{
		blocksDir:    blocksDir,
		shardID:      7,
		idx:          idx,
		upload:       UploadConfig{Enabled: true},
		blockUploads: newBlockUploadLifecycle(),
	}
	if err := s.writeSealIntentLocked(1, 10); err != nil {
		t.Fatalf("writeSealIntentLocked: %v", err)
	}
	if err := s.reconcileClosedBlockLocked(1); err != nil {
		t.Fatalf("reconcileClosedBlockLocked: %v", err)
	}
	if _, err := os.Stat(sealIntentPath(blocksDir, 1)); !os.IsNotExist(err) {
		t.Fatalf("seal intent should be removed after confirmed reconcile, stat=%v", err)
	}
}
