package index_test

import (
	"crypto/sha256"
	"path/filepath"
	"testing"

	"github.com/petabytecl/scrap/internal/index"
)

func TestPutGet(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Put("tx-001", 100, 3, true); err != nil {
		t.Fatalf("Put: %v", err)
	}

	entry, err := idx.Get("tx-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entry.BlockIDs) != 1 || entry.BlockIDs[0] != 100 {
		t.Fatalf("BlockIDs: got %v, want [100]", entry.BlockIDs)
	}
	if entry.DocCount != 3 {
		t.Fatalf("DocCount: got %d, want 3", entry.DocCount)
	}
	if !entry.Completed {
		t.Fatal("Completed should be true")
	}
}

func TestGetNotFound(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_, err = idx.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestAddBlockID(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Put("tx-multi", 100, 2, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := idx.AddBlockID("tx-multi", 200); err != nil {
		t.Fatalf("AddBlockID: %v", err)
	}

	entry, err := idx.Get("tx-multi")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(entry.BlockIDs) != 2 {
		t.Fatalf("BlockIDs length: got %d, want 2", len(entry.BlockIDs))
	}
	if entry.BlockIDs[0] != 100 || entry.BlockIDs[1] != 200 {
		t.Fatalf("BlockIDs: got %v, want [100 200]", entry.BlockIDs)
	}
}

func TestIncrementDocCount(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Put("tx-inc", 100, 0, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := idx.IncrementDocCount("tx-inc"); err != nil {
		t.Fatalf("IncrementDocCount: %v", err)
	}
	if err := idx.IncrementDocCount("tx-inc"); err != nil {
		t.Fatalf("IncrementDocCount: %v", err)
	}

	entry, err := idx.Get("tx-inc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.DocCount != 2 {
		t.Fatalf("DocCount: got %d, want 2", entry.DocCount)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if idx.Exists("tx-new") {
		t.Fatal("Exists should return false for missing key")
	}

	if err := idx.Put("tx-new", 100, 1, false); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !idx.Exists("tx-new") {
		t.Fatal("Exists should return true after Put")
	}
}

func TestStreamingHash_Determinism(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Put("tx-a", 1, 2, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.Put("tx-b", 3, 1, true); err != nil {
		t.Fatalf("Put: %v", err)
	}
	idx.SetAppliedIndex(42)

	ai1, hash1, err := idx.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash: %v", err)
	}
	if ai1 != 42 {
		t.Fatalf("appliedIndex: got %d, want 42", ai1)
	}
	if hash1 == [32]byte{} {
		t.Fatal("hash should not be zero for non-empty projection")
	}

	ai2, hash2, err := idx.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hashes differ on same data: %x vs %x", hash1, hash2)
	}
	if ai2 != 42 {
		t.Fatalf("appliedIndex: got %d, want 42", ai2)
	}
}

func TestPersistAppliedIndex_SurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	pebbleDir := filepath.Join(dir, "pebble")

	idx, err := index.Open(pebbleDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := idx.AppliedIndex(); got != 0 {
		t.Fatalf("fresh AppliedIndex: got %d, want 0", got)
	}
	if err := idx.PersistAppliedIndex(1234); err != nil {
		t.Fatalf("PersistAppliedIndex: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := index.Open(pebbleDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got := reopened.AppliedIndex(); got != 1234 {
		t.Fatalf("reopened AppliedIndex: got %d, want 1234", got)
	}
}

func TestPersistAppliedIndex_ExcludedFromStreamingHash(t *testing.T) {
	dir := t.TempDir()

	idx1, err := index.Open(filepath.Join(dir, "pebble1"))
	if err != nil {
		t.Fatalf("Open idx1: %v", err)
	}
	defer func() { _ = idx1.Close() }()
	idx2, err := index.Open(filepath.Join(dir, "pebble2"))
	if err != nil {
		t.Fatalf("Open idx2: %v", err)
	}
	defer func() { _ = idx2.Close() }()

	for _, idx := range []*index.Index{idx1, idx2} {
		if err := idx.Put("tx-a", 1, 2, false); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	// Same projection content, different durable applied indexes: the applied
	// index is per-Member restore bookkeeping and must not change the hash, or
	// cross-Member consistency checks would always diverge.
	if err := idx1.PersistAppliedIndex(10); err != nil {
		t.Fatalf("PersistAppliedIndex idx1: %v", err)
	}
	if err := idx2.PersistAppliedIndex(99); err != nil {
		t.Fatalf("PersistAppliedIndex idx2: %v", err)
	}

	_, hash1, err := idx1.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx1: %v", err)
	}
	_, hash2, err := idx2.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx2: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hash changed with durable applied index: %x vs %x", hash1, hash2)
	}
}

func TestStreamingHash_EmptyProjection(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	_, hash, err := idx.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash: %v", err)
	}

	emptyHash := sha256.Sum256(nil)
	if hash != emptyHash {
		t.Fatalf("empty hash: got %x, want %x", hash, emptyHash)
	}
}

func TestStreamingHash_DifferentData(t *testing.T) {
	dir := t.TempDir()

	idx1, err := index.Open(filepath.Join(dir, "pebble1"))
	if err != nil {
		t.Fatalf("Open idx1: %v", err)
	}
	defer func() { _ = idx1.Close() }()
	if err := idx1.Put("tx-a", 1, 2, false); err != nil {
		t.Fatalf("Put idx1: %v", err)
	}

	idx2, err := index.Open(filepath.Join(dir, "pebble2"))
	if err != nil {
		t.Fatalf("Open idx2: %v", err)
	}
	defer func() { _ = idx2.Close() }()
	if err := idx2.Put("tx-b", 3, 1, true); err != nil {
		t.Fatalf("Put idx2: %v", err)
	}

	_, hash1, err := idx1.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx1: %v", err)
	}
	_, hash2, err := idx2.StreamingHash()
	if err != nil {
		t.Fatalf("StreamingHash idx2: %v", err)
	}

	if hash1 == hash2 {
		t.Fatal("different data should produce different hashes")
	}
}

func TestIncrementDocCountRejectsOverflow(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Put("tx-full", 1, 65535, false); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := idx.IncrementDocCount("tx-full"); err == nil {
		t.Fatal("IncrementDocCount at 65535 succeeded, want overflow error")
	}
	entry, err := idx.Get("tx-full")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.DocCount != 65535 {
		t.Fatalf("DocCount after rejected increment: got %d, want 65535", entry.DocCount)
	}
}
